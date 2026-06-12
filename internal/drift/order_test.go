package drift

import (
	"reflect"
	"slices"
	"testing"
)

func TestApplyOrder(t *testing.T) {
	in := []string{"auth", "cloud", "sys", "switch:1", "switch:0", "mqtt"}
	want := []string{"sys", "switch:0", "switch:1", "mqtt", "cloud", "auth"}
	if got := ApplyOrder(in); !reflect.DeepEqual(got, want) {
		t.Errorf("ApplyOrder = %v, want %v", got, want)
	}
}

func TestApplyOrderUnknownBeforeAuth(t *testing.T) {
	in := []string{"auth", "ble"}
	want := []string{"ble", "auth"}
	if got := ApplyOrder(in); !reflect.DeepEqual(got, want) {
		t.Errorf("ApplyOrder = %v, want %v", got, want)
	}
}

func TestApplyOrderWifiLast(t *testing.T) {
	got := ApplyOrder([]string{"wifi", "auth", "sys", "switch:0", "future"})
	want := []string{"sys", "switch:0", "future", "auth", "wifi"}
	if !reflect.DeepEqual(got, want) {
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
