/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package mcs

import (
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lhconst "github.com/submariner-io/lighthouse/pkg/constants"
)

// Lighthouse provides a wrapper around Submariner's Lighthouse naming
// conventions. The type is not strictly needed (hence the lack of receiver
// object in all calls) but can be used a a scope for shorter function
// names.
type Lighthouse struct{}

// GenerateObjectName returns a canonical representation for a fully qualified object.
// The name should be treated as opaque (i.e., no assumption on the order or
// composition of the parts should be made by the caller).
// @todo consider handling when generated object name exceeds k8s limits
//	(e.g., use a crypto hash instead).
func (*Lighthouse) GenerateObjectName(name, ns, cluster string) string {
	return name + "-" + ns + "-" + cluster
}

// GetOriginalObjectName retrieves the original object name based on the
// Lighthouse labels provided in the object metadata.
func (*Lighthouse) GetOriginalObjectName(objmd metav1.ObjectMeta) types.NamespacedName {
	return types.NamespacedName{
		Namespace: objmd.GetLabels()[lhconst.LabelSourceNamespace],
		Name:      objmd.GetLabels()[lhconst.LighthouseLabelSourceName],
	}
}

// GetOriginalObjectCluster retrieves the original object cluster
// identifier based on the corresponding Lighthouse label.
func (*Lighthouse) GetOriginalObjectCluster(objmd metav1.ObjectMeta) string {
	return objmd.GetLabels()[lhconst.LighthouseLabelSourceCluster]
}

// Label the object with the expected source object identity labels.
// Note: metav1.SetMetaDataLabel added in v0.20.0
func (*Lighthouse) Label(objmd *metav1.ObjectMeta, name, ns, cluster string) {
	objmd.GetLabels()[lhconst.LighthouseLabelSourceCluster] = cluster
	objmd.GetLabels()[lhconst.LabelSourceNamespace] = ns
	objmd.GetLabels()[lhconst.LighthouseLabelSourceName] = name
}

// Annotate the object with the expected source object identity annotations.
func (*Lighthouse) Annotate(objmd *metav1.ObjectMeta, name, ns, cluster string) {
	metav1.SetMetaDataAnnotation(objmd, lhconst.OriginNamespace, ns)
	metav1.SetMetaDataAnnotation(objmd, lhconst.OriginName, name)
}

// ListFilter creates a filter that can be used to list only matching ServiceExport
// or ServiceImport objects. It relies on the presence of the Lighthouse labels.
func (*Lighthouse) ListFilter(objmd metav1.ObjectMeta) (*client.ListOptions, error) {
	if objmd.GetLabels()[lhconst.LabelSourceNamespace] == "" ||
		objmd.GetLabels()[lhconst.LighthouseLabelSourceName] == "" {
		return nil, errors.New("missing lighthouse labels")
	}

	opts := &client.ListOptions{}

	client.InNamespace(objmd.GetNamespace()).ApplyToList(opts)
	client.MatchingLabels{
		lhconst.LabelSourceNamespace:      objmd.GetLabels()[lhconst.LabelSourceNamespace],
		lhconst.LighthouseLabelSourceName: objmd.GetLabels()[lhconst.LighthouseLabelSourceName],
	}.ApplyToList(opts)
	return opts, nil
}
