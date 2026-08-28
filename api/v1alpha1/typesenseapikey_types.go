/*
Copyright 2024.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TypesenseClusterReference identifies a TypesenseCluster, optionally in another namespace.
type TypesenseClusterReference struct {
	// Name of the TypesenseCluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the TypesenseCluster. Defaults to the TypesenseApiKey's own namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// TypesenseApiKeySpec defines the desired state of TypesenseApiKey
type TypesenseApiKeySpec struct {
	// ClusterRef is the TypesenseCluster this key is issued against. If Namespace is omitted, the
	// TypesenseCluster is looked up in the TypesenseApiKey's own namespace.
	// +kubebuilder:validation:Required
	ClusterRef TypesenseClusterReference `json:"clusterRef"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Actions []string `json:"actions"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Collections []string `json:"collections"`

	// ExpiresAt maps to Typesense's expires_at (unix seconds).
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Value pins a specific key string instead of letting Typesense auto-generate one.
	// +optional
	Value *string `json:"value,omitempty"`
}

// TypesenseApiKeyStatus defines the observed state of TypesenseApiKey
type TypesenseApiKeyStatus struct {
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +optional
	Phase string `json:"phase,omitempty"`

	// KeyId is the numeric id Typesense assigned to this key, needed to delete/rotate it.
	// +optional
	KeyId *int64 `json:"keyId,omitempty"`

	// ValuePrefix is the redacted prefix Typesense returns when fetching a key, used for audit/drift display only.
	// +optional
	ValuePrefix string `json:"valuePrefix,omitempty"`

	// ObservedGeneration is the .metadata.generation last successfully reconciled into a Typesense key.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SecretRef is the name of the Secret holding the current plaintext key value.
	// +optional
	SecretRef corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TypesenseApiKey is the Schema for the typesenseapikeys API
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Key Id",type=integer,JSONPath=`.status.keyId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
type TypesenseApiKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TypesenseApiKeySpec   `json:"spec,omitempty"`
	Status TypesenseApiKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TypesenseApiKeyList contains a list of TypesenseApiKey
type TypesenseApiKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TypesenseApiKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TypesenseApiKey{}, &TypesenseApiKeyList{})
}
