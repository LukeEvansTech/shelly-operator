package shelly_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func hostOf(url string) string { return strings.TrimPrefix(url, "http://") }

func TestProbe(t *testing.T) {
	d := &shellytest.Device{
		ID: "shellyplusplug-3c8a1fec8e3c", MAC: "3C8A1FEC8E3C",
		Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		Firmware: "20241011-114449/1.4.4-g6d2a586", Name: "PDU-01", Password: "secret",
	}
	srv := shellytest.New(d)
	defer srv.Close()

	info, err := shelly.Probe(context.Background(), nil, hostOf(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	want := shelly.DeviceInfo{
		ID: "shellyplusplug-3c8a1fec8e3c", MAC: "3C8A1FEC8E3C",
		Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		Firmware: "20241011-114449/1.4.4-g6d2a586", AuthEnabled: true, Name: "PDU-01",
	}
	if *info != want {
		t.Errorf("Probe() = %+v, want %+v", *info, want)
	}
}

func TestProbeUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := shelly.Probe(ctx, nil, "127.0.0.1:1"); err == nil {
		t.Error("expected error for unreachable host")
	}
}

func TestProbeNonShellyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if _, err := shelly.Probe(context.Background(), nil, hostOf(srv.URL)); err == nil {
		t.Error("expected error for non-Shelly 404 response")
	}
}
