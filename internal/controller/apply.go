package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// sectionAuth is the pseudo-section name for Shelly.SetAuth.
const sectionAuth = "auth"

// applyResult reports what enforcement wrote to a device.
type applyResult struct {
	applied         []string // sections written, in apply order
	restartRequired bool
}

// applyFindings writes the drifted sections to the device, safest-first
// with auth always last (see drift.ApplyOrder). It stops at the first
// failure and returns what was applied so far; callers surface the error
// on the InSync condition. The auth pseudo-section is special-cased to
// Shelly.SetAuth and needs the device id (fetched via probe) plus the
// resolved password.
func (r *ShellyDeviceReconciler) applyFindings(ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	desired map[string]map[string]any, findings []drift.Finding, authEnable *bool, password string) (applyResult, error) {

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
