package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// stubReader serves ConfigMaps/Secrets from maps; nil maps yield NotFound.
type stubReader struct {
	client.Reader
	cms     map[string]*corev1.ConfigMap
	secrets map[string]*corev1.Secret
	err     error
}

func (s stubReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if s.err != nil {
		return s.err
	}
	switch o := obj.(type) {
	case *corev1.ConfigMap:
		if cm, ok := s.cms[key.Name]; ok {
			*o = *cm
			return nil
		}
		return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, key.Name)
	case *corev1.Secret:
		if sec, ok := s.secrets[key.Name]; ok {
			*o = *sec
			return nil
		}
		return apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
	}
	return fmt.Errorf("unexpected type %T", obj)
}

func dev(displayName string) *shellyv1alpha1.ShellyDevice {
	return &shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "aabbccddee01"},
		Spec:       shellyv1alpha1.ShellyDeviceSpec{DisplayName: displayName},
	}
}

func TestResolveNameDisplayNameWins(t *testing.T) {
	r := stubReader{cms: map[string]*corev1.ConfigMap{"shelly-names": {Data: map[string]string{"aabbccddee01": "from-map"}}}}
	got, err := ResolveName(context.Background(), r, dev("front-desk"), "shelly-names")
	if err != nil || got != "front-desk" {
		t.Errorf("got %q, %v; want front-desk", got, err)
	}
}

func TestResolveNameFromMap(t *testing.T) {
	r := stubReader{cms: map[string]*corev1.ConfigMap{"shelly-names": {Data: map[string]string{"aabbccddee01": "rack-pdu"}}}}
	got, err := ResolveName(context.Background(), r, dev(""), "shelly-names")
	if err != nil || got != "rack-pdu" {
		t.Errorf("got %q, %v; want rack-pdu", got, err)
	}
}

func TestResolveNameMissingMapIsEmpty(t *testing.T) {
	got, err := ResolveName(context.Background(), stubReader{}, dev(""), "shelly-names")
	if err != nil || got != "" {
		t.Errorf("got %q, %v; want empty, nil", got, err)
	}
}

func TestResolveNameReadErrorPropagates(t *testing.T) {
	r := stubReader{err: fmt.Errorf("boom")}
	if _, err := ResolveName(context.Background(), r, dev(""), "shelly-names"); err == nil || !strings.Contains(err.Error(), "name map") {
		t.Errorf("want wrapped error, got %v", err)
	}
}

func TestResolveNameDisabled(t *testing.T) {
	got, err := ResolveName(context.Background(), stubReader{err: fmt.Errorf("must not be called")}, dev(""), "")
	if err != nil || got != "" {
		t.Errorf("empty map name must disable lookups: %q, %v", got, err)
	}
}

func TestResolveWifiPasswords(t *testing.T) {
	r := stubReader{secrets: map[string]*corev1.Secret{
		"wifi-creds": {Data: map[string][]byte{"new": []byte("hunter2"), "old": []byte("legacy")}},
	}}

	t.Run("nil section resolves empty", func(t *testing.T) {
		got, err := ResolveWifiPasswords(context.Background(), r, "ns", nil)
		if err != nil || got != (WifiPasswords{}) {
			t.Fatalf("got %+v, err %v", got, err)
		}
	})

	t.Run("both networks resolve", func(t *testing.T) {
		w := &shellyv1alpha1.WifiSection{
			Sta:  &shellyv1alpha1.WifiNetwork{PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi-creds", Key: "new"}},
			Sta1: &shellyv1alpha1.WifiNetwork{PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi-creds", Key: "old"}},
		}
		got, err := ResolveWifiPasswords(context.Background(), r, "ns", w)
		if err != nil {
			t.Fatal(err)
		}
		if got.Sta != "hunter2" || got.Sta1 != "legacy" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("network without ref resolves empty", func(t *testing.T) {
		w := &shellyv1alpha1.WifiSection{Sta: &shellyv1alpha1.WifiNetwork{SSID: "open-net"}}
		got, err := ResolveWifiPasswords(context.Background(), r, "ns", w)
		if err != nil || got.Sta != "" {
			t.Fatalf("got %+v, err %v", got, err)
		}
	})

	t.Run("missing key is an error", func(t *testing.T) {
		w := &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi-creds", Key: "nope"}},
		}
		_, err := ResolveWifiPasswords(context.Background(), r, "ns", w)
		if err == nil || !strings.Contains(err.Error(), `no key "nope"`) {
			t.Fatalf("want missing-key error, got %v", err)
		}
	})

	t.Run("missing secret is an error", func(t *testing.T) {
		w := &shellyv1alpha1.WifiSection{
			Sta1: &shellyv1alpha1.WifiNetwork{PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "absent", Key: "k"}},
		}
		_, err := ResolveWifiPasswords(context.Background(), r, "ns", w)
		if err == nil {
			t.Fatal("want error for missing secret")
		}
	})

	t.Run("nil reader resolves empty", func(t *testing.T) {
		w := &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi-creds", Key: "new"}},
		}
		got, err := ResolveWifiPasswords(context.Background(), nil, "ns", w)
		if err != nil || got != (WifiPasswords{}) {
			t.Fatalf("got %+v, err %v", got, err)
		}
	})
}

func TestResolvePassword(t *testing.T) {
	auth := &shellyv1alpha1.AuthSection{PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "creds", Key: "password"}}
	r := stubReader{secrets: map[string]*corev1.Secret{"creds": {Data: map[string][]byte{"password": []byte("hunter2")}}}}
	got, err := ResolvePassword(context.Background(), r, "ns", auth)
	if err != nil || got != "hunter2" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := ResolvePassword(context.Background(), stubReader{}, "ns", auth); err == nil {
		t.Error("missing secret must error")
	}
	auth2 := &shellyv1alpha1.AuthSection{PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "creds", Key: "nope"}}
	if _, err := ResolvePassword(context.Background(), r, "ns", auth2); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("missing key must error naming the key, got %v", err)
	}
	if got, err := ResolvePassword(context.Background(), r, "ns", nil); err != nil || got != "" {
		t.Errorf("nil auth must be empty, nil: %q %v", got, err)
	}
}
