# shelly-operator

A Kubernetes operator for GitOps fleet management of Shelly Gen2+ smart
plugs: subnet-sweep discovery into `ShellyDevice` objects, declarative
`ShellyProfile` config with continuous drift reconciliation over the Shelly
JSON-RPC API, a device-list ConfigMap feed for
[shelly_exporter](https://github.com/LukeEvansTech/shelly_exporter), and a
read-only dashboard.

Status: early development. Currently implemented: `internal/shelly` RPC
client (probe, JSON-RPC, SHA-256 digest auth, config get/set, SetAuth)
with a fake-device test server; `ShellyDevice` CRD and subnet-sweep
discovery (`--discovery-cidrs`); `ShellyProfile` CRD with drift detection
and enforce-mode correction (safest-first apply, auth password rollout
from Secrets, non-convergence damping); the shelly_exporter device-list
ConfigMap feed (`--exporter-configmap`); and a read-only dashboard
(`--dashboard-bind`, default :8090) showing fleet state, per-device drift
diffs, and profile matching; and wifi management (`spec.config.wifi` with
sta/sta1 networks and passwords from Secrets). MQTT credentials and
firmware updates are not implemented.

## Description

### WiFi migration

`spec.config.wifi` manages the device's client networks: `sta` (primary)
and `sta1` (the device's fallback when sta is unreachable). Passwords are
read from Secrets via `passSecretRef`, are never rendered, diffed, or
displayed (devices treat them as write-only), and are injected only at
apply time. Wifi is applied last of all sections -- after auth -- because
it can move the device to another network.

To migrate a fleet to a new network, point `sta` at the new network and
keep `sta1` pointed at the old one: if the sta rollout is wrong, devices
fall back to sta1 instead of being stranded (the controller warns when
`sta` is managed without a `sta1` fallback). If a device becomes
unreachable right after a wifi write, its InSync condition goes
Unknown/`WifiApplied` until discovery re-finds it at its new address --
so `--discovery-cidrs` must cover BOTH the old and new subnets for the
duration of the migration. See
`config/samples/shelly_v1alpha1_shellyprofile.yaml` for a worked example.

### Firmware auto-update

`spec.config.firmware.autoUpdate: true` keeps devices on the latest
STABLE firmware using the device's own scheduler: the operator ensures
an enabled schedule job calling `Shelly.Update {stage: stable}` exists
(daily at 00:00 device-local time, identical to the job the Shelly app
creates). Any enabled stable-update job satisfies the check regardless
of its timespec, so devices configured via the app are compliant
without rewrites; jobs targeting other stages (beta) are drift and are
deleted under enforce. `autoUpdate: false` enforces the absence of
update jobs; other schedule jobs are never touched.

Pending updates are visible in `status.availableFirmware` (and
`kubectl get shellydevices -o wide`), refreshed by the discovery sweep.
Caution: a firmware update reboots the device, and after reboot a
switch output follows its `initial_state` -- with `initial_state: off`
the load stays off until something turns it back on. Consider managing
`spec.config.switch.initialState: restore_last` alongside auto-update.

## Install (Helm)

The chart and image are published to GHCR on each release:

```sh
helm install shelly-operator oci://ghcr.io/lukeevanstech/charts/shelly-operator \
  --version 0.1.0 \
  --namespace shelly-operator --create-namespace \
  --set 'discovery.cidrs={10.32.8.0/24}'
```

Key values (see `charts/shelly-operator/values.yaml` for the full list):

- `discovery.cidrs`: IPv4 subnets swept for Shelly devices. Empty list
  disables discovery.
- `exporterConfigMap`: name of the ConfigMap rendered as a device-list
  feed for shelly_exporter. Empty string disables the feed.
- `dashboard.enabled` / `dashboard.bind`: the read-only dashboard
  (default on, `:8090`).

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/shelly-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don't work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/shelly-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/shelly-operator:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/shelly-operator/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

