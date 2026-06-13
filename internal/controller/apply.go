package controller

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/fleet"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// sectionAuth is the pseudo-section name for Shelly.SetAuth.
const sectionAuth = "auth"

// sectionWifi is the wifi component section; special-cased because writes
// inject resolved network passwords and a post-apply recheck failure means
// the device may have moved networks (see runEnforce).
const sectionWifi = "wifi"

// applyResult reports what enforcement wrote to a device.
type applyResult struct {
	applied         []string // sections written, in apply order
	restartRequired bool
}

// applyFindings writes the drifted sections to the device, safest-first
// with auth second-to-last and wifi dead last (see drift.ApplyOrder). It
// stops at the first failure and returns what was applied so far; callers
// surface the error on the InSync condition. The auth pseudo-section is
// special-cased to Shelly.SetAuth and needs the device id (fetched via
// probe) plus the resolved password. The wifi section is special-cased to
// inject the resolved network passwords via wifiPayload before the write.
// The firmware pseudo-section is special-cased to applyFirmware (schedule-job writes).
func (r *ShellyDeviceReconciler) applyFindings(ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	desired map[string]map[string]any, findings []drift.Finding, authEnable, fwEnable *bool, password string, wifiPw fleet.WifiPasswords) (applyResult, error) {

	var res applyResult
	for _, section := range drift.ApplyOrder(drift.Sections(findings)) {
		switch {
		case section == sectionAuth:
			targetOn := authEnable != nil && *authEnable
			if targetOn && password == "" {
				return res, fmt.Errorf("auth: no password available (set spec.config.auth.passwordSecretRef)")
			}
			info, err := shelly.Probe(ctx, r.rpcHTTPClient(), dev.Status.Address)
			if err != nil {
				return res, fmt.Errorf("auth: probing for device id: %w", err)
			}
			pw := password
			if !targetOn {
				pw = "" // disable auth
			}
			if err := c.SetAuth(ctx, info.ID, pw); err != nil {
				return res, fmt.Errorf("auth: %w", err)
			}
		case section == sectionFirmware:
			if fwEnable == nil {
				// Cannot happen: findings only exist when the profile
				// manages the section. Defensive, like auth's password check.
				return res, fmt.Errorf("firmware: section drifted but profile does not manage it")
			}
			if err := applyFirmware(ctx, c, *fwEnable); err != nil {
				return res, fmt.Errorf("firmware: %w", err)
			}
		case section == sectionWifi:
			restart, err := c.SetConfig(ctx, section, wifiPayload(desired[section], wifiPw))
			if err != nil {
				return res, fmt.Errorf("%s: %w", section, err)
			}
			res.restartRequired = res.restartRequired || restart
		case strings.HasPrefix(section, "switch:"):
			id, err := strconv.Atoi(strings.TrimPrefix(section, "switch:"))
			if err != nil {
				return res, fmt.Errorf("%s: bad component id: %w", section, err)
			}
			restart, err := c.SetSwitchConfig(ctx, id, desired[section])
			if err != nil {
				return res, fmt.Errorf("%s: %w", section, err)
			}
			res.restartRequired = res.restartRequired || restart
		default:
			restart, err := c.SetConfig(ctx, section, desired[section])
			if err != nil {
				return res, fmt.Errorf("%s: %w", section, err)
			}
			res.restartRequired = res.restartRequired || restart
		}
		res.applied = append(res.applied, section)
	}
	return res, nil
}

// wifiPayload returns a copy of the rendered wifi section with the
// resolved network passwords injected. Passwords are never rendered (not
// diffable, and rendered output is dashboard-visible), but the device
// needs them whenever a protected network's config is written. The input
// is not mutated.
func wifiPayload(rendered map[string]any, pw fleet.WifiPasswords) map[string]any {
	out := make(map[string]any, len(rendered))
	for k, v := range rendered {
		if m, ok := v.(map[string]any); ok {
			cp := make(map[string]any, len(m)+1)
			maps.Copy(cp, m)
			// Deep-copy one more level so nested objects (e.g.
			// ap.range_extender) are not aliased back into the caller's
			// rendered map -- the input must not be mutated.
			for nk, nv := range m {
				if nm, ok := nv.(map[string]any); ok {
					ncp := make(map[string]any, len(nm))
					maps.Copy(ncp, nm)
					cp[nk] = ncp
				}
			}
			out[k] = cp
			continue
		}
		out[k] = v
	}
	inject := func(key, pass string) {
		if pass == "" {
			return
		}
		if m, ok := out[key].(map[string]any); ok {
			m["pass"] = pass
		}
	}
	inject("sta", pw.Sta)
	inject("sta1", pw.Sta1)
	return out
}
