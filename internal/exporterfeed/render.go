// Package exporterfeed renders and maintains the ConfigMap that
// shelly_exporter consumes, replacing the old discover-script ->
// 1Password inventory pipeline.
package exporterfeed

import (
	"slices"

	"sigs.k8s.io/yaml"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// Options are the non-inventory parts of shelly_exporter's config file.
type Options struct {
	ListenAddress        string
	Debug                bool
	DeviceUpdateInterval int
}

type exporterConfig struct {
	ListenAddress        string           `json:"listenAddress"`
	Debug                bool             `json:"debug"`
	DeviceUpdateInterval int              `json:"deviceUpdateInterval"`
	Devices              []exporterDevice `json:"devices"`
}

type exporterDevice struct {
	Host string `json:"host"`
}

// RenderConfig produces shelly_exporter's config.yaml for the online
// devices, sorted by address for deterministic output (no spurious
// ConfigMap updates).
func RenderConfig(devs []shellyv1alpha1.ShellyDevice, opts Options) (string, error) {
	hosts := make([]string, 0, len(devs))
	for i := range devs {
		if devs[i].Status.Online && devs[i].Status.Address != "" {
			hosts = append(hosts, devs[i].Status.Address)
		}
	}
	slices.Sort(hosts)
	hosts = slices.Compact(hosts)
	cfg := exporterConfig{
		ListenAddress:        opts.ListenAddress,
		Debug:                opts.Debug,
		DeviceUpdateInterval: opts.DeviceUpdateInterval,
		Devices:              make([]exporterDevice, 0, len(hosts)),
	}
	for _, h := range hosts {
		cfg.Devices = append(cfg.Devices, exporterDevice{Host: h})
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
