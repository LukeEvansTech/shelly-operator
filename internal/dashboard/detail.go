package dashboard

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/fleet"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

var (
	deviceTmpl   = template.Must(template.ParseFS(tmplFS, "templates/layout.html", "templates/device.html"))
	profilesTmpl = template.Must(template.ParseFS(tmplFS, "templates/layout.html", "templates/profiles.html"))
)

type deviceView struct {
	Object, Name, Model, Address, Firmware, Profile, Sync, SyncClass, Message string
	Gen                                                                       int32
	Online                                                                    bool
	Findings                                                                  []drift.Finding
	Desired, Actual                                                           string
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var dev shellyv1alpha1.ShellyDevice
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: r.PathValue("name")}, &dev); err != nil {
		if apierrors.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sync, class := syncState(&dev)
	view := deviceView{
		Object: dev.Name, Model: dev.Status.Model, Address: dev.Status.Address,
		Firmware: dev.Status.Firmware, Profile: dev.Status.MatchedProfile,
		Gen: dev.Status.Gen, Online: dev.Status.Online, Sync: sync, SyncClass: class,
	}
	if c := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync); c != nil {
		view.Message = c.Message
	}

	var profiles shellyv1alpha1.ShellyProfileList
	if err := s.Client.List(ctx, &profiles, client.InNamespace(s.Namespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profile, _ := drift.MatchProfile(&dev, profiles.Items)

	name, err := fleet.ResolveName(ctx, s.Reader, &dev, s.NameMapName)
	if err == nil {
		view.Name = name
	}

	// Live diff only when reachable and governed by a profile.
	if dev.Status.Online && dev.Status.Address != "" && profile != nil {
		password, _ := fleet.ResolvePassword(ctx, s.Reader, dev.Namespace, profile.Spec.Config.Auth)
		opts := []shelly.Option{shelly.WithHTTPClient(s.HTTP)}
		if password != "" {
			opts = append(opts, shelly.WithPassword(password))
		}
		c := shelly.NewClient(dev.Status.Address, opts...)
		if actual, err := c.GetConfig(ctx); err == nil {
			desired := drift.Render(profile.Spec.Config, name, actual)
			findings, diffErr := drift.Diff(desired, actual)
			if diffErr != nil {
				view.Message = fmt.Sprintf("diff failed: %v", diffErr)
			} else {
				view.Findings = findings
			}
			view.Desired = mustJSON(desired)
			actualTree := map[string]any{}
			for k, raw := range actual {
				var v any
				if err := json.Unmarshal(raw, &v); err == nil {
					if m, ok := v.(map[string]any); ok {
						actualTree[k] = redactSecrets(m)
					} else {
						actualTree[k] = v
					}
				}
			}
			view.Actual = mustJSON(actualTree)
		} else {
			view.Message = fmt.Sprintf("live config fetch failed: %v", err)
		}
	}

	render(w, deviceTmpl, view)
}

type profileRow struct {
	Name, Mode string
	Priority   int32
	Matched    int
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var profiles shellyv1alpha1.ShellyProfileList
	if err := s.Client.List(ctx, &profiles, client.InNamespace(s.Namespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var devs shellyv1alpha1.ShellyDeviceList
	if err := s.Client.List(ctx, &devs, client.InNamespace(s.Namespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	counts := map[string]int{}
	for i := range devs.Items {
		if p, _ := drift.MatchProfile(&devs.Items[i], profiles.Items); p != nil {
			counts[p.Name]++
		}
	}
	rows := make([]profileRow, 0, len(profiles.Items))
	for i := range profiles.Items {
		p := &profiles.Items[i]
		rows = append(rows, profileRow{Name: p.Name, Mode: p.Spec.Mode, Priority: p.Spec.Priority, Matched: counts[p.Name]})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Name < rows[b].Name })
	render(w, profilesTmpl, map[string]any{"Profiles": rows})
}

// secretKeyPattern matches config keys whose values must never render.
// Over-redaction is harmless on a read-only display; future firmware may
// add fields we have not seen.
var secretKeyPattern = regexp.MustCompile(`(?i)pass|token|secret|key`)

// redactSecrets returns a deep copy of m with values of secret-looking
// keys replaced, recursing into nested maps and arrays. The input is not
// mutated.
func redactSecrets(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if secretKeyPattern.MatchString(k) && v != nil {
			out[k] = "[redacted]"
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return redactSecrets(t)
	case []any:
		cp := make([]any, len(t))
		for i, e := range t {
			cp[i] = redactValue(e)
		}
		return cp
	default:
		return v
	}
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("render error: %v", err)
	}
	return string(b)
}
