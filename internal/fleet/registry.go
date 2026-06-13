package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// RegistryEntry holds the per-device metadata parsed from the registry
// ConfigMap. All fields are optional; zero value means absent.
type RegistryEntry struct {
	Name string `json:"name"`
	Room string `json:"room"`
	Type string `json:"type"`
	Note string `json:"note"`
}

// ResolveRegistry looks up dev.Name in the registry ConfigMap and returns the
// parsed RegistryEntry. registryName "" disables the registry (returns zero).
// A missing ConfigMap is treated as empty (returns zero, no error). Any other
// read error or JSON parse error is returned so a transient failure cannot
// masquerade as "no entry".
func ResolveRegistry(ctx context.Context, reader client.Reader, dev *shellyv1alpha1.ShellyDevice, registryName string) (RegistryEntry, error) {
	if registryName == "" || reader == nil {
		return RegistryEntry{}, nil
	}
	var cm corev1.ConfigMap
	if err := reader.Get(ctx, types.NamespacedName{Namespace: dev.Namespace, Name: registryName}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return RegistryEntry{}, nil
		}
		return RegistryEntry{}, fmt.Errorf("reading registry %s/%s: %w", dev.Namespace, registryName, err)
	}
	raw, ok := cm.Data[dev.Name]
	if !ok || raw == "" {
		return RegistryEntry{}, nil
	}
	var entry RegistryEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return RegistryEntry{}, fmt.Errorf("parsing registry entry for %s: %w", dev.Name, err)
	}
	return entry, nil
}
