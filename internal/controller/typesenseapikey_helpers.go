package controller

import (
	"context"
	"fmt"
	"slices"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseApiKeyReconciler) resolveCluster(ctx context.Context, key *tsv1alpha1.TypesenseApiKey) (*tsv1alpha1.TypesenseCluster, error) {
	namespace := key.Spec.ClusterRef.Namespace
	if namespace == "" {
		namespace = key.Namespace
	}

	var ts tsv1alpha1.TypesenseCluster
	clusterObjectKey := client.ObjectKey{Namespace: namespace, Name: key.Spec.ClusterRef.Name}

	if err := r.Get(ctx, clusterObjectKey, &ts); err != nil {
		return nil, fmt.Errorf("resolving typesense cluster %s: %w", clusterObjectKey, err)
	}

	return &ts, nil
}

// getAdminApiKey fetches the plaintext admin api key of the referenced TypesenseCluster, using
// the same object-key resolution logic as typesensecluster_secret.go:getAdminApiKeyObjectKey.
func (r *TypesenseApiKeyReconciler) getAdminApiKey(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) ([]byte, error) {
	secretObjectKey := adminApiKeySecretObjectKey(ts)

	var secret v1.Secret
	if err := r.Get(ctx, secretObjectKey, &secret); err != nil {
		return nil, fmt.Errorf("fetching admin api key secret %s: %w", secretObjectKey, err)
	}

	adminKey, ok := secret.Data[ClusterAdminApiKeySecretKeyName]
	if !ok {
		return nil, fmt.Errorf("admin api key secret %s is missing key %q", secretObjectKey, ClusterAdminApiKeySecretKeyName)
	}

	return adminKey, nil
}

// keySpecMatchesRemote reports whether the CR's spec still matches what Typesense holds for the
// key, ignoring order in the actions/collections lists. expires_at and value are not compared:
// Typesense's GET /keys/{id} never returns the plaintext value, and expiry drift is harmless
// (Typesense enforces it server-side regardless of what's mirrored into status).
func keySpecMatchesRemote(key *tsv1alpha1.TypesenseApiKey, remote *KeyResponse) bool {
	if key.Spec.Description != remote.Description {
		return false
	}

	return stringSetsEqual(key.Spec.Actions, remote.Actions) && stringSetsEqual(key.Spec.Collections, remote.Collections)
}

func stringSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

func adminApiKeySecretObjectKey(ts *tsv1alpha1.TypesenseCluster) client.ObjectKey {
	if ts.Spec.AdminApiKey != nil {
		return client.ObjectKey{
			Namespace: ts.Namespace,
			Name:      ts.Spec.AdminApiKey.Name,
		}
	}

	return client.ObjectKey{
		Namespace: ts.Namespace,
		Name:      fmt.Sprintf(ClusterAdminApiKeySecret, ts.Name),
	}
}
