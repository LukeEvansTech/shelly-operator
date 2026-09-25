package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// updateSettle is how long after a request the operator leaves a device
// alone before asking again for the same version. A real install takes one to
// three minutes and the device reports the old version until it reboots, so
// without this every in-window reconcile would re-send the request mid-flash.
const updateSettle = 20 * time.Minute

// updateIfAvailable reports whether it sent an update the device may now be
// installing; Reconcile then stops touching the device for this cycle.
//
// updateIfAvailable installs a pending stable firmware update when the matched
// profile opted in (updateWhenAvailable), in enforce mode, inside its
// updateWindow.
//
// The device's own 00:00 Shelly.Update schedule job is left in place; this is
// the path that can SEE a failure. The device's answer -- accepted, or an RPC
// error such as -114 "No update info" -- is written to
// status.lastFirmwareUpdate, which outlives an Event.
//
// Only one device starts an update per UpdateSpacing, fleet-wide. That keeps
// the fleet from fetching the same image from the vendor at the same second,
// which is the one thing the on-device job cannot avoid (every job fires at
// 00:00:00), and keeps load reboots from landing all at once.
//
// Like rebootIfRequested it acts only on THIS cycle's successful
// Sys.GetStatus read, never on a status value carried over.
func (r *ShellyDeviceReconciler) updateIfAvailable(
	ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	profile *shellyv1alpha1.ShellyProfile, sys sysRead,
) (sent bool) {
	if profile == nil || !profile.Spec.UpdateWhenAvailable || profile.Spec.Mode != shellyv1alpha1.ModeEnforce {
		return false
	}
	if !sys.fresh || sys.availableFirmware == "" {
		return false
	}
	if profile.Spec.UpdateWindow == nil {
		// The CRD rejects this; defence for objects written around it. No
		// window must never mean "any time".
		return false
	}
	ok, err := withinRebootWindow(time.Now(), profile.Spec.UpdateWindow)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Event(dev, corev1.EventTypeWarning, "UpdateWindowInvalid",
				fmt.Sprintf("not updating firmware: %v", err))
		}
		return false
	}
	if !ok {
		return false
	}
	if last := dev.Status.LastFirmwareUpdate; last != nil && !last.Refused &&
		last.Target == sys.availableFirmware && time.Since(last.Time.Time) < updateSettle {
		return false
	}
	if !r.claimUpdateSlot(ctx, dev.Namespace) {
		return false
	}

	// Persist the attempt BEFORE the call, so the slot and the settle gate
	// hold even if this process dies between the device accepting and the
	// answer being recorded. If it cannot be persisted, do not send.
	from := dev.Status.Firmware
	attempt := &shellyv1alpha1.FirmwareUpdateAttempt{
		Time:   metav1.Now(),
		From:   from,
		Target: sys.availableFirmware,
	}
	if !r.recordUpdateAttempt(ctx, dev, attempt) {
		return false
	}

	callErr := c.Update(ctx, "stable")
	if callErr == nil {
		if r.Recorder != nil {
			r.Recorder.Event(dev, corev1.EventTypeNormal, "FirmwareUpdateStarted",
				fmt.Sprintf("requested stable firmware %s (running %s)", sys.availableFirmware, from))
		}
		return true
	}
	done := attempt.DeepCopy()
	done.Error = callErr.Error()
	done.Refused = shelly.IsRefusal(callErr)
	if r.Recorder != nil {
		msg := fmt.Sprintf("Shelly.Update to %s refused: %v", sys.availableFirmware, callErr)
		if !done.Refused {
			msg = fmt.Sprintf("Shelly.Update to %s did not answer cleanly; it may be installing: %v",
				sys.availableFirmware, callErr)
		}
		r.Recorder.Event(dev, corev1.EventTypeWarning, "FirmwareUpdateFailed", msg)
	}
	r.recordUpdateAttempt(ctx, dev, done)
	return !done.Refused
}

// recordUpdateAttempt persists the attempt, retrying once on a fresh copy.
// It matters more than most status writes: the settle gate and the
// fleet-wide spacing both read it back, so losing it can mean a second
// Shelly.Update sent to a device that is mid-flash.
func (r *ShellyDeviceReconciler) recordUpdateAttempt(
	ctx context.Context, dev *shellyv1alpha1.ShellyDevice, attempt *shellyv1alpha1.FirmwareUpdateAttempt,
) bool {
	base := dev.DeepCopy()
	dev.Status.LastFirmwareUpdate = attempt
	if err := r.Status().Patch(ctx, dev, client.MergeFrom(base)); err == nil {
		return true
	}
	var fresh shellyv1alpha1.ShellyDevice
	if err := r.Get(ctx, client.ObjectKeyFromObject(dev), &fresh); err == nil {
		freshBase := fresh.DeepCopy()
		fresh.Status.LastFirmwareUpdate = attempt
		if err := r.Status().Patch(ctx, &fresh, client.MergeFrom(freshBase)); err == nil {
			return true
		}
	}
	if r.Recorder != nil {
		r.Recorder.Event(dev, corev1.EventTypeWarning, "FirmwareUpdateNotRecorded",
			"could not persist status.lastFirmwareUpdate")
	}
	dev.Status.LastFirmwareUpdate = base.Status.LastFirmwareUpdate
	return false
}

// updateInFlight reports whether the device accepted an update recently and
// is presumably still downloading or flashing it, and if so how long to wait.
// Reconcile checks it before touching the device at all, so no config write
// or reboot lands mid-install. It lifts as soon as discovery reports a
// different firmware, or after updateSettle if the install never happened.
func updateInFlight(dev *shellyv1alpha1.ShellyDevice, now time.Time) (bool, time.Duration) {
	last := dev.Status.LastFirmwareUpdate
	if last == nil || last.Refused || dev.Status.Firmware != last.From {
		return false, 0
	}
	remaining := updateSettle - now.Sub(last.Time.Time)
	if remaining <= 0 {
		return false, 0
	}
	return true, remaining
}

// claimUpdateSlot reports whether an update may start now, and if so records
// it. The slot is claimed before the call is sent, so a slow or failing
// device still holds it and concurrent reconciles cannot both go.
//
// The in-memory time covers concurrent workers in this process; the newest
// persisted status.lastFirmwareUpdate in the namespace covers a restart or a
// leader handover, which would otherwise reset the spacing to zero.
func (r *ShellyDeviceReconciler) claimUpdateSlot(ctx context.Context, namespace string) bool {
	spacing := r.UpdateSpacing
	if spacing <= 0 {
		spacing = 2 * time.Minute
	}
	var devs shellyv1alpha1.ShellyDeviceList
	if err := r.List(ctx, &devs, client.InNamespace(namespace)); err != nil {
		return false // cannot prove the fleet is quiet; wait for the next reconcile
	}
	r.updateMu.Lock()
	defer r.updateMu.Unlock()
	latest := r.lastUpdateStart
	for i := range devs.Items {
		if a := devs.Items[i].Status.LastFirmwareUpdate; a != nil && a.Time.After(latest) {
			latest = a.Time.Time
		}
	}
	now := time.Now()
	if !latest.IsZero() && now.Sub(latest) < spacing {
		return false
	}
	r.lastUpdateStart = now
	return true
}
