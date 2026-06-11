// Package fleet holds resolution helpers shared by the device controller
// and the dashboard: desired device names and admin passwords.
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

// ResolveName returns the desired device name: spec.displayName first,
// falling back to the name-map ConfigMap (keyed by the device object
// name, i.e. lowercased MAC). nameMapName "" disables the map. A missing
// ConfigMap means "" (unmanaged); any other read error propagates so a
// transient API failure can't masquerade as in-sync.
func ResolveName(ctx context.Context, reader client.Reader, dev *shellyv1alpha1.ShellyDevice, nameMapName string) (string, error) {
	if dev.Spec.DisplayName != "" {
		return dev.Spec.DisplayName, nil
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
	ref := auth.PasswordSecretRef
	var secret corev1.Secret
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return "", fmt.Errorf("reading password secret %s/%s: %w", namespace, ref.Name, err)
	}
	pw, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("password secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	return string(pw), nil
}
