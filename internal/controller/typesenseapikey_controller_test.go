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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

const (
	testTypesenseImage        = "typesense/typesense:27.1"
	testStorageClassName      = "standard"
	testDocumentsSearchAction = "documents:search"
)

// fakeKeysServer is a minimal in-memory stand-in for the Typesense /keys REST API, used so the
// TypesenseApiKeyReconciler tests can exercise real HTTP request/response handling without a
// live Typesense cluster. It keeps created keys in memory so GET can reflect drift (or a
// disappearance) injected directly through forget()/mutate() to simulate out-of-band changes.
type fakeKeysServer struct {
	mu      sync.Mutex
	nextId  int64
	keys    map[int64]KeyResponse
	deleted []int64
}

func newFakeKeysServer() (*httptest.Server, *fakeKeysServer) {
	f := &fakeKeysServer{nextId: 1, keys: map[int64]KeyResponse{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req CreateKeyRequest
		Expect(json.NewDecoder(r.Body).Decode(&req)).To(Succeed())

		f.mu.Lock()
		id := f.nextId
		f.nextId++

		value := fmt.Sprintf("secret-value-%d", id)
		if req.Value != nil {
			value = *req.Value
		}

		resp := KeyResponse{
			Id:          id,
			Description: req.Description,
			Actions:     req.Actions,
			Collections: req.Collections,
			Value:       value,
			ValuePrefix: value[:4],
		}
		f.keys[id] = resp
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		Expect(json.NewEncoder(w).Encode(resp)).To(Succeed())
	})
	mux.HandleFunc("/keys/", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/keys/"), 10, 64)
		Expect(err).NotTo(HaveOccurred())

		switch r.Method {
		case http.MethodGet:
			f.mu.Lock()
			resp, ok := f.keys[id]
			f.mu.Unlock()

			if !ok {
				w.WriteHeader(http.StatusNotFound)
				Expect(json.NewEncoder(w).Encode(typesenseApiError{Message: "key not found"})).To(Succeed())
				return
			}

			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(resp)).To(Succeed())
		case http.MethodDelete:
			f.mu.Lock()
			delete(f.keys, id)
			f.deleted = append(f.deleted, id)
			f.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(deleteKeyResponse{Id: id})).To(Succeed())
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	return httptest.NewServer(mux), f
}

// forget simulates the remote key having disappeared out-of-band (e.g. deleted directly through
// the Typesense API, bypassing this operator).
func (f *fakeKeysServer) forget(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.keys, id)
}

// mutate simulates the remote key having been edited out-of-band.
func (f *fakeKeysServer) mutate(id int64, fn func(resp KeyResponse) KeyResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[id] = fn(f.keys[id])
}

// redirectTransport forwards every request to targetURL regardless of what host/scheme the
// request was originally built for - buildKeysUrl always targets a k8s in-cluster Service DNS
// name that doesn't resolve in envtest, so requests are rewritten onto the fakeKeysServer instead.
type redirectTransport struct {
	targetURL *url.URL
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.targetURL.Scheme
	req.URL.Host = t.targetURL.Host
	req.Host = t.targetURL.Host
	return http.DefaultTransport.RoundTrip(req)
}

var _ = Describe("TypesenseApiKey Controller", func() {
	const clusterName = "apikey-test-cluster"
	const namespace = "default"

	ctx := context.Background()

	var (
		server          *httptest.Server
		fakeKeys        *fakeKeysServer
		reconciler      *TypesenseApiKeyReconciler
		cluster         *tsv1alpha1.TypesenseCluster
		adminSecretName string
	)

	BeforeEach(func() {
		server, fakeKeys = newFakeKeysServer()
		targetURL, err := url.Parse(server.URL)
		Expect(err).NotTo(HaveOccurred())

		cluster = &tsv1alpha1.TypesenseCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: tsv1alpha1.TypesenseClusterSpec{
				Image: testTypesenseImage,
				Storage: &tsv1alpha1.StorageSpec{
					StorageClassName: testStorageClassName,
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		adminSecretName = fmt.Sprintf(ClusterAdminApiKeySecret, clusterName)
		adminSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      adminSecretName,
				Namespace: namespace,
			},
			Data: map[string][]byte{
				ClusterAdminApiKeySecretKeyName: []byte("admin-secret-key"),
			},
		}
		Expect(k8sClient.Create(ctx, adminSecret)).To(Succeed())

		reconciler = &TypesenseApiKeyReconciler{
			Client:     k8sClient,
			Scheme:     k8sClient.Scheme(),
			HttpClient: &http.Client{Transport: &redirectTransport{targetURL: targetURL}},
		}
	})

	AfterEach(func() {
		server.Close()

		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: adminSecretName, Namespace: namespace},
		})).To(Succeed())
	})

	It("creates a remote key and a Secret holding its value, then cleans both up on delete", func() {
		key := &tsv1alpha1.TypesenseApiKey{
			ObjectMeta: metav1.ObjectMeta{Name: "test-key", Namespace: namespace},
			Spec: tsv1alpha1.TypesenseApiKeySpec{
				ClusterRef:  tsv1alpha1.TypesenseClusterReference{Name: clusterName},
				Description: "test key",
				Actions:     []string{testDocumentsSearchAction},
				Collections: []string{"*"},
			},
		}
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		nn := types.NamespacedName{Name: key.Name, Namespace: namespace}

		By("adding the finalizer on the first reconcile")
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		By("creating the remote key on the second reconcile")
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		var got tsv1alpha1.TypesenseApiKey
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(got.Status.KeyId).NotTo(BeNil())
		Expect(*got.Status.KeyId).To(Equal(int64(1)))
		Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		Expect(meta.IsStatusConditionTrue(got.Status.Conditions, ApiKeyConditionTypeReady)).To(BeTrue())

		var secret corev1.Secret
		secretKey := types.NamespacedName{Name: fmt.Sprintf(ApiKeySecretName, key.Name), Namespace: namespace}
		Expect(k8sClient.Get(ctx, secretKey, &secret)).To(Succeed())
		Expect(string(secret.Data[ApiKeySecretKeyName])).To(Equal("secret-value-1"))

		By("deleting the remote key when the CR is deleted")
		Expect(k8sClient.Delete(ctx, &got)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		Expect(errors.IsNotFound(k8sClient.Get(ctx, nn, &got))).To(BeTrue())
		Expect(fakeKeys.deleted).To(ConsistOf(int64(1)))
	})

	It("rotates the remote key when the spec changes", func() {
		key := &tsv1alpha1.TypesenseApiKey{
			ObjectMeta: metav1.ObjectMeta{Name: "rotate-key", Namespace: namespace},
			Spec: tsv1alpha1.TypesenseApiKeySpec{
				ClusterRef:  tsv1alpha1.TypesenseClusterReference{Name: clusterName},
				Description: "rotate key",
				Actions:     []string{testDocumentsSearchAction},
				Collections: []string{"*"},
			},
		}
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		nn := types.NamespacedName{Name: key.Name, Namespace: namespace}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		var got tsv1alpha1.TypesenseApiKey
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(*got.Status.KeyId).To(Equal(int64(1)))
		firstGeneration := got.Generation

		By("changing the spec so a new generation is observed")
		got.Spec.Collections = []string{"other-collection"}
		Expect(k8sClient.Update(ctx, &got)).To(Succeed())
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(got.Generation).To(BeNumerically(">", firstGeneration))

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(*got.Status.KeyId).To(Equal(int64(2)))
		Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		Expect(fakeKeys.deleted).To(ConsistOf(int64(1)))

		var secret corev1.Secret
		secretKey := types.NamespacedName{Name: fmt.Sprintf(ApiKeySecretName, key.Name), Namespace: namespace}
		Expect(k8sClient.Get(ctx, secretKey, &secret)).To(Succeed())
		Expect(string(secret.Data[ApiKeySecretKeyName])).To(Equal("secret-value-2"))

		Expect(k8sClient.Delete(ctx, &got)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
	})

	It("recreates the remote key when it disappears out-of-band", func() {
		key := &tsv1alpha1.TypesenseApiKey{
			ObjectMeta: metav1.ObjectMeta{Name: "drift-missing-key", Namespace: namespace},
			Spec: tsv1alpha1.TypesenseApiKeySpec{
				ClusterRef:  tsv1alpha1.TypesenseClusterReference{Name: clusterName},
				Description: "drift key",
				Actions:     []string{testDocumentsSearchAction},
				Collections: []string{"*"},
			},
		}
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		nn := types.NamespacedName{Name: key.Name, Namespace: namespace}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		var got tsv1alpha1.TypesenseApiKey
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(*got.Status.KeyId).To(Equal(int64(1)))

		By("deleting the key directly through the fake Typesense API")
		fakeKeys.forget(1)

		By("healing the drift on the next steady-state reconcile")
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(*got.Status.KeyId).To(Equal(int64(2)))
		Expect(meta.IsStatusConditionTrue(got.Status.Conditions, ApiKeyConditionTypeReady)).To(BeTrue())

		Expect(k8sClient.Delete(ctx, &got)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
	})

	It("rotates the remote key when it was edited out-of-band", func() {
		key := &tsv1alpha1.TypesenseApiKey{
			ObjectMeta: metav1.ObjectMeta{Name: "drift-mutated-key", Namespace: namespace},
			Spec: tsv1alpha1.TypesenseApiKeySpec{
				ClusterRef:  tsv1alpha1.TypesenseClusterReference{Name: clusterName},
				Description: "drift key",
				Actions:     []string{testDocumentsSearchAction},
				Collections: []string{"*"},
			},
		}
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		nn := types.NamespacedName{Name: key.Name, Namespace: namespace}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		var got tsv1alpha1.TypesenseApiKey
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(*got.Status.KeyId).To(Equal(int64(1)))

		By("editing the key's actions directly through the fake Typesense API")
		fakeKeys.mutate(1, func(resp KeyResponse) KeyResponse {
			resp.Actions = []string{"documents:*"}
			return resp
		})

		By("healing the drift on the next steady-state reconcile")
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(*got.Status.KeyId).To(Equal(int64(2)))
		Expect(fakeKeys.deleted).To(ConsistOf(int64(1)))
		Expect(meta.IsStatusConditionTrue(got.Status.Conditions, ApiKeyConditionTypeReady)).To(BeTrue())

		Expect(k8sClient.Delete(ctx, &got)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a key for a TypesenseCluster in a different namespace", func() {
		otherNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "apikey-cross-ns-test"},
		}
		Expect(k8sClient.Create(ctx, otherNamespace)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, otherNamespace)).To(Succeed())
		}()

		remoteClusterName := "cross-ns-cluster"
		remoteCluster := &tsv1alpha1.TypesenseCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      remoteClusterName,
				Namespace: otherNamespace.Name,
			},
			Spec: tsv1alpha1.TypesenseClusterSpec{
				Image: testTypesenseImage,
				Storage: &tsv1alpha1.StorageSpec{
					StorageClassName: testStorageClassName,
				},
			},
		}
		Expect(k8sClient.Create(ctx, remoteCluster)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, remoteCluster)).To(Succeed())
		}()

		remoteAdminSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf(ClusterAdminApiKeySecret, remoteClusterName),
				Namespace: otherNamespace.Name,
			},
			Data: map[string][]byte{
				ClusterAdminApiKeySecretKeyName: []byte("admin-secret-key"),
			},
		}
		Expect(k8sClient.Create(ctx, remoteAdminSecret)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, remoteAdminSecret)).To(Succeed())
		}()

		key := &tsv1alpha1.TypesenseApiKey{
			ObjectMeta: metav1.ObjectMeta{Name: "cross-ns-key", Namespace: namespace},
			Spec: tsv1alpha1.TypesenseApiKeySpec{
				ClusterRef: tsv1alpha1.TypesenseClusterReference{
					Name:      remoteClusterName,
					Namespace: otherNamespace.Name,
				},
				Description: "cross namespace key",
				Actions:     []string{testDocumentsSearchAction},
				Collections: []string{"*"},
			},
		}
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		nn := types.NamespacedName{Name: key.Name, Namespace: namespace}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())

		var got tsv1alpha1.TypesenseApiKey
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		Expect(got.Status.KeyId).NotTo(BeNil())
		Expect(meta.IsStatusConditionTrue(got.Status.Conditions, ApiKeyConditionTypeReady)).To(BeTrue())

		By("writing the Secret in the TypesenseApiKey's own namespace, not the cluster's")
		var secret corev1.Secret
		secretKey := types.NamespacedName{Name: fmt.Sprintf(ApiKeySecretName, key.Name), Namespace: namespace}
		Expect(k8sClient.Get(ctx, secretKey, &secret)).To(Succeed())

		Expect(k8sClient.Delete(ctx, &got)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
	})
})
