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

package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

// apiKeyReconcileRequeuePeriod is how often a steady-state (already-created, unchanged) key is
// re-checked for drift.
var apiKeyReconcileRequeuePeriod = 5 * time.Minute

// TypesenseApiKeyReconciler reconciles a TypesenseApiKey object
type TypesenseApiKeyReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	logger     logr.Logger
	Recorder   record.EventRecorder
	HttpClient *http.Client
}

// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseapikeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseapikeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseapikeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop for TypesenseApiKey. It handles
// first-ever create, spec-change rotation, steady-state drift detection, and finalizer-guarded
// delete.
func (r *TypesenseApiKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.Log.WithValues("namespace", req.Namespace, "apikey", req.Name)

	var key tsv1alpha1.TypesenseApiKey
	if err := r.Get(ctx, req.NamespacedName, &key); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	r.logger.Info("reconciling api key")

	if !key.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &key)
	}

	if !controllerutil.ContainsFinalizer(&key, ApiKeyFinalizer) {
		controllerutil.AddFinalizer(&key, ApiKeyFinalizer)
		if err := r.Update(ctx, &key); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue immediately (not RequeueAfter apiKeyReconcileRequeuePeriod like the rest of this
		// function) so the finalizer add is picked straight back up instead of waiting out the
		// steady-state period.
		return ctrl.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: RequeueAfter has no immediate-requeue equivalent
	}

	if err := r.initConditions(ctx, &key); err != nil {
		return ctrl.Result{}, err
	}

	ts, err := r.resolveCluster(ctx, &key)
	if err != nil {
		r.logger.Error(err, "resolving typesense cluster failed")
		if cerr := r.setConditionNotReady(ctx, &key, ApiKeyConditionReasonClusterNotFound, err); cerr != nil {
			return ctrl.Result{}, cerr
		}
		return ctrl.Result{RequeueAfter: apiKeyReconcileRequeuePeriod}, nil
	}

	adminKey, err := r.getAdminApiKey(ctx, ts)
	if err != nil {
		r.logger.Error(err, "resolving admin api key failed")
		if cerr := r.setConditionNotReady(ctx, &key, ApiKeyConditionReasonAdminKeySecretNotReady, err); cerr != nil {
			return ctrl.Result{}, cerr
		}
		return ctrl.Result{RequeueAfter: apiKeyReconcileRequeuePeriod}, nil
	}

	switch {
	case key.Status.KeyId == nil:
		if err := r.createApiKey(ctx, &key, ts, adminKey); err != nil {
			r.logger.Error(err, "creating typesense api key failed")
			if cerr := r.setConditionNotReady(ctx, &key, ApiKeyConditionReasonKeyCreateFailed, err); cerr != nil {
				return ctrl.Result{}, cerr
			}
			return ctrl.Result{RequeueAfter: apiKeyReconcileRequeuePeriod}, nil
		}
	case key.Generation != key.Status.ObservedGeneration:
		if err := r.rotateApiKey(ctx, &key, ts, adminKey); err != nil {
			r.logger.Error(err, "rotating typesense api key failed")
			if cerr := r.setConditionNotReady(ctx, &key, ApiKeyConditionReasonKeyRotateFailed, err); cerr != nil {
				return ctrl.Result{}, cerr
			}
			return ctrl.Result{RequeueAfter: apiKeyReconcileRequeuePeriod}, nil
		}
	default:
		if err := r.checkDrift(ctx, &key, ts, adminKey); err != nil {
			r.logger.Error(err, "checking typesense api key for drift failed")
			if cerr := r.setConditionNotReady(ctx, &key, ApiKeyConditionReasonKeyDriftCheckFailed, err); cerr != nil {
				return ctrl.Result{}, cerr
			}
			return ctrl.Result{RequeueAfter: apiKeyReconcileRequeuePeriod}, nil
		}
	}

	if err := r.setConditionReady(ctx, &key, ApiKeyConditionReasonReady); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: apiKeyReconcileRequeuePeriod}, nil
}

// createApiKey creates a brand-new Typesense key from the CR's spec, stores its value in a
// Secret, and records the result in status.
func (r *TypesenseApiKeyReconciler) createApiKey(ctx context.Context, key *tsv1alpha1.TypesenseApiKey, ts *tsv1alpha1.TypesenseCluster, adminKey []byte) error {
	var expiresAt *int64
	if key.Spec.ExpiresAt != nil {
		unix := key.Spec.ExpiresAt.Unix()
		expiresAt = &unix
	}

	created, err := r.createKey(ctx, ts, adminKey, CreateKeyRequest{
		Description: key.Spec.Description,
		Actions:     key.Spec.Actions,
		Collections: key.Spec.Collections,
		ExpiresAt:   expiresAt,
		Value:       key.Spec.Value,
	})
	if err != nil {
		return err
	}

	secret, err := r.ReconcileSecret(ctx, key, created.Value)
	if err != nil {
		return err
	}

	return r.patchStatus(ctx, key, func(status *tsv1alpha1.TypesenseApiKeyStatus) {
		status.KeyId = &created.Id
		status.ValuePrefix = created.ValuePrefix
		status.ObservedGeneration = key.Generation
		status.SecretRef = corev1.LocalObjectReference{Name: secret.Name}
	})
}

// rotateApiKey is invoked when the CR's spec has changed since the last successful reconcile
// (key.Generation != key.Status.ObservedGeneration). Typesense keys are immutable, so rotation
// means deleting the old remote key and creating a new one from the current spec, then updating
// the Secret and status to point at it. The old key is deleted first so a stale key from a
// removed action/collection never outlives its replacement.
func (r *TypesenseApiKeyReconciler) rotateApiKey(ctx context.Context, key *tsv1alpha1.TypesenseApiKey, ts *tsv1alpha1.TypesenseCluster, adminKey []byte) error {
	if key.Status.KeyId != nil {
		if err := r.deleteKey(ctx, ts, adminKey, *key.Status.KeyId); err != nil {
			return err
		}
	}

	return r.createApiKey(ctx, key, ts, adminKey)
}

// checkDrift re-fetches the remote key on every steady-state reconcile (at
// apiKeyReconcileRequeuePeriod cadence) and heals it if it was deleted or edited out-of-band,
// e.g. directly through the Typesense API: a missing key is recreated, a key whose
// description/actions/collections no longer match the spec is rotated.
func (r *TypesenseApiKeyReconciler) checkDrift(ctx context.Context, key *tsv1alpha1.TypesenseApiKey, ts *tsv1alpha1.TypesenseCluster, adminKey []byte) error {
	remote, err := r.getKey(ctx, ts, adminKey, *key.Status.KeyId)
	if err != nil {
		if !isNotFoundErr(err) {
			return err
		}

		r.logger.Info("remote typesense api key no longer exists, recreating", "keyId", *key.Status.KeyId)
		return r.createApiKey(ctx, key, ts, adminKey)
	}

	if keySpecMatchesRemote(key, remote) {
		return nil
	}

	r.logger.Info("remote typesense api key drifted from spec, rotating", "keyId", *key.Status.KeyId)
	return r.rotateApiKey(ctx, key, ts, adminKey)
}

// reconcileDelete deletes the corresponding Typesense key (best-effort - tolerates the owning
// cluster or its admin key already being gone, e.g. because the cluster was deleted first) before
// removing the finalizer so the CR can actually be garbage collected. A genuine failure to reach
// an otherwise-resolvable cluster is returned as an error so the finalizer stays and this gets
// retried, rather than risking an orphaned remote key.
func (r *TypesenseApiKeyReconciler) reconcileDelete(ctx context.Context, key *tsv1alpha1.TypesenseApiKey) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(key, ApiKeyFinalizer) {
		return ctrl.Result{}, nil
	}

	if key.Status.KeyId != nil {
		ts, err := r.resolveCluster(ctx, key)
		if err != nil {
			r.logger.Info("typesense cluster no longer resolvable, skipping remote key deletion", "reason", err.Error())
		} else if adminKey, err := r.getAdminApiKey(ctx, ts); err != nil {
			r.logger.Info("admin api key no longer resolvable, skipping remote key deletion", "reason", err.Error())
		} else if err := r.deleteKey(ctx, ts, adminKey, *key.Status.KeyId); err != nil {
			r.logger.Error(err, "deleting typesense api key failed")
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(key, ApiKeyFinalizer)
	if err := r.Update(ctx, key); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *TypesenseApiKeyReconciler) initConditions(ctx context.Context, key *tsv1alpha1.TypesenseApiKey) error {
	if len(key.Status.Conditions) == 0 {
		if err := r.patchStatus(ctx, key, func(status *tsv1alpha1.TypesenseApiKeyStatus) {
			meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
				Type:    ApiKeyConditionTypeReady,
				Status:  metav1.ConditionUnknown,
				Reason:  ApiKeyConditionReasonReconciliationInProgress,
				Message: ApiKeyInitReconciliationMessage,
			})
			status.Phase = "Pending"
		}); err != nil {
			r.logger.Error(err, ApiKeyUpdateStatusMessageFailed)
			return err
		}
	}
	return nil
}

func (r *TypesenseApiKeyReconciler) setConditionNotReady(ctx context.Context, key *tsv1alpha1.TypesenseApiKey, reason string, err error) error {
	return r.patchStatus(ctx, key, func(status *tsv1alpha1.TypesenseApiKeyStatus) {
		meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
			Type:    ApiKeyConditionTypeReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: err.Error(),
		})
		status.Phase = reason
	})
}

func (r *TypesenseApiKeyReconciler) setConditionReady(ctx context.Context, key *tsv1alpha1.TypesenseApiKey, reason string) error {
	return r.patchStatus(ctx, key, func(status *tsv1alpha1.TypesenseApiKeyStatus) {
		meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
			Type:    ApiKeyConditionTypeReady,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: "Api Key is Ready",
		})
		status.Phase = reason
	})
}

func (r *TypesenseApiKeyReconciler) patchStatus(
	ctx context.Context,
	key *tsv1alpha1.TypesenseApiKey,
	patcher func(status *tsv1alpha1.TypesenseApiKeyStatus),
) error {
	patch := client.MergeFrom(key.DeepCopy())
	patcher(&key.Status)

	if err := r.Status().Patch(ctx, key, patch); err != nil {
		r.logger.Error(err, "unable to patch typesense api key status")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TypesenseApiKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tsv1alpha1.TypesenseApiKey{}, eventFilters).
		Named("typesense-apikey-controller").
		Complete(r)
}
