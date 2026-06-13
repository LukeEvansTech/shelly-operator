package drift

import (
	"slices"
	"testing"
)

func TestApplyOrder(t *testing.T) {
	in := []string{"auth", "cloud", "sys", "switch:1", "switch:0", "mqtt"}
	want := []string{"sys", "switch:0", "switch:1", "mqtt", "cloud", "auth"}
	if got := ApplyOrder(in); !slices.Equal(got, want) {
		t.Errorf("ApplyOrder = %v, want %v", got, want)
	}
}

func TestApplyOrderBLEBeforeAuth(t *testing.T) {
	// ble is an explicitly-ranked section (3, alongside cloud), well before auth.
	in := []string{"auth", "ble", "cloud"}
	want := []string{"ble", "cloud", "auth"}
	if got := ApplyOrder(in); !slices.Equal(got, want) {
		t.Errorf("ApplyOrder = %v, want %v", got, want)
	}
}

func TestApplyOrderWifiLast(t *testing.T) {
	got := ApplyOrder([]string{"wifi", "auth", "sys", "switch:0", "future"})
	want := []string{"sys", "switch:0", "future", "auth", "wifi"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyOrderDoesNotMutateInput(t *testing.T) {
	in := []string{"auth", "sys"}
	_ = ApplyOrder(in)
	if in[0] != "auth" {
		t.Error("input slice must not be reordered in place")
	}
}

func TestApplyOrderFirmwareBeforeAuthAndWifi(t *testing.T) {
	got := ApplyOrder([]string{"wifi", "auth", "firmware", "cloud", "sys"})
	want := []string{"sys", "cloud", "firmware", "auth", "wifi"}
	if !slices.Equal(got, want) {
		t.Fatalf("ApplyOrder = %v, want %v", got, want)
	}
}

func TestApplyOrderUIWithSwitches(t *testing.T) {
	// *_ui sections rank 1 (same as switch:*), sorted alphabetically within rank.
	got := ApplyOrder([]string{"mqtt", "pluguk_ui", "switch:0", "sys"})
	want := []string{"sys", "pluguk_ui", "switch:0", "mqtt"}
	if !slices.Equal(got, want) {
		t.Fatalf("ApplyOrder = %v, want %v", got, want)
	}
}
