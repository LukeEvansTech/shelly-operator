// Package fleet holds resolution helpers shared by the device controller
// and the dashboard: desired device names, admin passwords, and wifi
// network passwords.
package fleet

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// ResolveName returns the desired device name using the priority:
//
//  1. spec.displayName (user override, always wins)
//  2. registry ConfigMap entry's "name" field (registryName "shelly-registry")
//  3. name-map ConfigMap entry (nameMapName "shelly-names")
//
// Either name is "" to disable the corresponding lookup. A missing ConfigMap
// means "" (unmanaged); any other read error propagates so a transient API
// failure can't masquerade as in-sync.
func ResolveName(ctx context.Context, reader client.Reader, dev *shellyv1alpha1.ShellyDevice, nameMapName, registryName string) (string, error) {
	if dev.Spec.DisplayName != "" {
		return dev.Spec.DisplayName, nil
	}
	// Registry name takes priority over name-map.
	if registryName != "" && reader != nil {
		entry, err := ResolveRegistry(ctx, reader, dev, registryName)
		if err != nil {
			return "", err
		}
		if entry.Name != "" {
			return entry.Name, nil
		}
	}
	if nameMapName == "" || reader == nil {
		return "", nil
	}
	var cm corev1.ConfigMap
	if err := reader.Get(ctx, types.NamespacedName{Namespace: dev.Namespace, Name: nameMapName}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading name map %s/%s: %w", dev.Namespace, nameMapName, err)
	}
	return cm.Data[dev.Name], nil
}

// ResolvePassword returns the device admin password from the profile's
// auth passwordSecretRef ("" when no ref configured). Read failures are
// errors -- a configured ref that cannot be read must not silently
// degrade to "no password".
func ResolvePassword(ctx context.Context, reader client.Reader, namespace string, auth *shellyv1alpha1.AuthSection) (string, error) {
	if auth == nil || auth.PasswordSecretRef == nil || reader == nil {
		return "", nil
	}
	return secretValue(ctx, reader, namespace, auth.PasswordSecretRef, "password")
}

// WifiPasswords holds resolved WiFi network passwords ("" = none declared).
type WifiPasswords struct {
	Sta  string
	Sta1 string
}

// ResolveWifiPasswords resolves the wifi section's network passwords from
// their Secret refs. Networks without a passSecretRef resolve to "". Read
// failures are errors -- a configured ref that cannot be read must not
// silently degrade to "no password".
func ResolveWifiPasswords(ctx context.Context, reader client.Reader, namespace string, wifi *shellyv1alpha1.WifiSection) (WifiPasswords, error) {
	var out WifiPasswords
	if wifi == nil || reader == nil {
		return out, nil
	}
	var err error
	if wifi.Sta != nil && wifi.Sta.PassSecretRef != nil {
		if out.Sta, err = secretValue(ctx, reader, namespace, wifi.Sta.PassSecretRef, "wifi sta password"); err != nil {
			return WifiPasswords{}, err
		}
	}
	if wifi.Sta1 != nil && wifi.Sta1.PassSecretRef != nil {
		if out.Sta1, err = secretValue(ctx, reader, namespace, wifi.Sta1.PassSecretRef, "wifi sta1 password"); err != nil {
			return WifiPasswords{}, err
		}
	}
	return out, nil
}

// secretValue reads one key from a Secret. what names the value in errors
// ("password", "wifi sta password", ...).
func secretValue(ctx context.Context, reader client.Reader, namespace string, ref *shellyv1alpha1.SecretKeyRef, what string) (string, error) {
	var secret corev1.Secret
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return "", fmt.Errorf("reading %s secret %s/%s: %w", what, namespace, ref.Name, err)
	}
	v, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("%s secret %s/%s has no key %q", what, namespace, ref.Name, ref.Key)
	}
	return string(v), nil
}
