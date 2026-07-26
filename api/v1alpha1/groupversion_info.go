/*
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
*/

// Package v1alpha1 contains API Schema definitions for the shelly v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=shelly.thirdimpact.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// SchemeGroupVersion is group version used to register these objects.
	// This name is used by applyconfiguration generators (e.g. controller-gen).
	SchemeGroupVersion = schema.GroupVersion{Group: "shelly.thirdimpact.io", Version: "v1alpha1"}

	// GroupVersion is an alias for SchemeGroupVersion, for backward compatibility.
	GroupVersion = SchemeGroupVersion

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	//
	// controller-runtime v0.24 deprecated scheme.Builder. The stated reason is
	// dependency hygiene, not correctness: "This helper is only useful in api
	// packages, but api packages should be easy to import and hence have minimal
	// dependencies." The helper still works and is not scheduled for removal, and
	// kubebuilder's own scaffold still generates exactly this pattern — so
	// hand-rolling an apimachinery-only replacement here would diverge from the
	// scaffold and put CRD type registration (the thing every controller and every
	// envtest suite depends on) at risk for an advisory lint. Suppressed
	// deliberately; worth revisiting as its own change, not inside a dependency bump.
	//nolint:staticcheck // SA1019: scheme.Builder deprecation is advisory (import weight); see above
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
