// Package dashboard serves the operator's read-only web UI: fleet state,
// per-device drift detail, and profile matching. It never writes to
// devices or the cluster -- git is the only write path. Secret values are
// never rendered. No app-level auth (network/ingress protected).
package dashboard

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

//go:embed templates/*.html
var tmplFS embed.FS

var fleetTmpl = template.Must(template.ParseFS(tmplFS, "templates/layout.html", "templates/fleet.html"))

// Server is the dashboard. It implements manager.Runnable and runs on
// every replica (read-only; no leader election needed).
type Server struct {
	Client      client.Client // cached
	Reader      client.Reader // uncached (name map, secrets)
	HTTP        *http.Client  // device RPC for the detail view; nil = 10s default
	Namespace   string
	NameMapName string
	Addr        string // listen address, e.g. ":8090"
}

// NeedLeaderElection: the dashboard serves reads on every replica.
func (s *Server) NeedLeaderElection() bool { return false }

// Start runs the HTTP server until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{Addr: s.Addr, Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	logf.FromContext(ctx).WithName("dashboard").Info("serving", "addr", s.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleFleet)
	mux.HandleFunc("GET /device/{name}", s.handleDevice)
	mux.HandleFunc("GET /profiles", s.handleProfiles)
	return mux
}

type fleetRow struct {
	Object, Name, Model, Address, Profile, Sync, SyncClass, Drift string
	Online                                                        bool
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	var devs shellyv1alpha1.ShellyDeviceList
	if err := s.Client.List(r.Context(), &devs, client.InNamespace(s.Namespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]fleetRow, 0, len(devs.Items))
	for i := range devs.Items {
		d := &devs.Items[i]
		sync, class := syncState(d)
		name := d.Spec.DisplayName
		if name == "" {
			name = d.Status.DeviceName
		}
		rows = append(rows, fleetRow{
			Object: d.Name, Name: name, Model: d.Status.Model, Address: d.Status.Address,
			Online: d.Status.Online, Profile: d.Status.MatchedProfile,
			Sync: sync, SyncClass: class, Drift: strings.Join(d.Status.DriftedSections, ", "),
		})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Object < rows[b].Object })
	render(w, fleetTmpl, map[string]any{"Devices": rows})
}

// syncState summarizes the InSync condition as label + pill class.
func syncState(d *shellyv1alpha1.ShellyDevice) (string, string) {
	c := meta.FindStatusCondition(d.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if c == nil {
		return "unknown", "unk"
	}
	switch c.Status {
	case metav1.ConditionTrue:
		return c.Reason, "ok"
	case metav1.ConditionFalse:
		return c.Reason, "bad"
	default:
		return c.Reason, "unk"
	}
}

func render(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleDevice and handleProfiles are implemented in detail.go (next
// task); these stubs keep the mux routable.
func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented yet", http.StatusNotImplemented)
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented yet", http.StatusNotImplemented)
}
