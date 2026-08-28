package controller

import (
	"context"
	"fmt"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReconcileSecret ensures a Secret holding the current plaintext key value exists and matches
// value. Unlike the TypesenseCluster admin key secret, this Secret is not immutable: rotation
// updates it in place.
func (r *TypesenseApiKeyReconciler) ReconcileSecret(ctx context.Context, key *tsv1alpha1.TypesenseApiKey, value string) (*v1.Secret, error) {
	secretObjectKey := getApiKeySecretObjectKey(key)

	var secret v1.Secret
	err := r.Get(ctx, secretObjectKey, &secret)
	if err != nil && !apierrors.IsNotFound(err) {
		r.logger.Error(err, "unable to fetch secret", "secret", secretObjectKey)
		return nil, err
	}

	if apierrors.IsNotFound(err) {
		secret = v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretObjectKey.Name,
				Namespace: secretObjectKey.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "typesense-operator",
					"app.kubernetes.io/name":       "typesense-api-key",
					"app.kubernetes.io/instance":   key.Name,
				},
			},
			Type: v1.SecretTypeOpaque,
			Data: map[string][]byte{
				ApiKeySecretKeyName: []byte(value),
			},
		}

		if err := ctrl.SetControllerReference(key, &secret, r.Scheme); err != nil {
			return nil, err
		}

		if err := r.Create(ctx, &secret); err != nil {
			r.logger.Error(err, "creating api key secret failed", "secret", secretObjectKey)
			return nil, err
		}

		return &secret, nil
	}

	if string(secret.Data[ApiKeySecretKeyName]) != value {
		secret.Data[ApiKeySecretKeyName] = []byte(value)
		if err := r.Update(ctx, &secret); err != nil {
			r.logger.Error(err, "updating api key secret failed", "secret", secretObjectKey)
			return nil, err
		}
	}

	return &secret, nil
}

func getApiKeySecretObjectKey(key *tsv1alpha1.TypesenseApiKey) client.ObjectKey {
	return client.ObjectKey{
		Namespace: key.Namespace,
		Name:      fmt.Sprintf(ApiKeySecretName, key.Name),
	}
}
