package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

// CreateKeyRequest is the request body for POST /keys.
type CreateKeyRequest struct {
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
	Collections []string `json:"collections"`
	ExpiresAt   *int64   `json:"expires_at,omitempty"`
	Value       *string  `json:"value,omitempty"`
}

// KeyResponse is the response body Typesense returns for POST /keys and GET /keys/{id}.
// Value is only ever populated on the POST /keys response - Typesense never returns it again afterwards.
type KeyResponse struct {
	Id          int64    `json:"id"`
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
	Collections []string `json:"collections"`
	ExpiresAt   int64    `json:"expires_at,omitempty"`
	Value       string   `json:"value,omitempty"`
	ValuePrefix string   `json:"value_prefix,omitempty"`
}

// deleteKeyResponse is the response body Typesense returns for DELETE /keys/{id}.
type deleteKeyResponse struct {
	Id int64 `json:"id"`
}

// typesenseApiError is the response body Typesense returns for non-2xx responses.
type typesenseApiError struct {
	Message string `json:"message"`
}

func (r *TypesenseApiKeyReconciler) createKey(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, adminKey []byte, req CreateKeyRequest) (*KeyResponse, error) {
	u, err := r.buildKeysUrl(ts, TypesenseKeysPath)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var keyResponse KeyResponse
	if err := r.doKeysRequest(ctx, http.MethodPost, u, adminKey, body, &keyResponse); err != nil {
		return nil, err
	}

	return &keyResponse, nil
}

func (r *TypesenseApiKeyReconciler) getKey(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, adminKey []byte, id int64) (*KeyResponse, error) {
	u, err := r.buildKeysUrl(ts, fmt.Sprintf("%s/%d", TypesenseKeysPath, id))
	if err != nil {
		return nil, err
	}

	var keyResponse KeyResponse
	if err := r.doKeysRequest(ctx, http.MethodGet, u, adminKey, nil, &keyResponse); err != nil {
		return nil, err
	}

	return &keyResponse, nil
}

// deleteKey deletes a key by id. A 404 from Typesense (key already gone) is treated as success.
func (r *TypesenseApiKeyReconciler) deleteKey(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, adminKey []byte, id int64) error {
	u, err := r.buildKeysUrl(ts, fmt.Sprintf("%s/%d", TypesenseKeysPath, id))
	if err != nil {
		return err
	}

	var resp deleteKeyResponse
	err = r.doKeysRequest(ctx, http.MethodDelete, u, adminKey, nil, &resp)
	if err != nil && isNotFoundErr(err) {
		return nil
	}

	return err
}

func (r *TypesenseApiKeyReconciler) doKeysRequest(ctx context.Context, method string, u string, adminKey []byte, body []byte, out any) error {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("x-typesense-api-key", string(adminKey))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.HttpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr typesenseApiError
		_ = json.Unmarshal(respBody, &apiErr)
		return &keysApiError{statusCode: resp.StatusCode, message: apiErr.Message, body: string(respBody)}
	}

	if len(respBody) == 0 {
		return nil
	}

	return json.Unmarshal(respBody, out)
}

// buildKeysUrl targets the cluster's ClusterIP REST Service, since key CRUD is cluster-wide
// state and does not need to go through any specific pod. Only supports in-cluster manager
// execution today - see plan notes on out-of-cluster dev mode.
func (r *TypesenseApiKeyReconciler) buildKeysUrl(ts *tsv1alpha1.TypesenseCluster, path string) (string, error) {
	svc := fmt.Sprintf("%s.%s.svc.cluster.local", fmt.Sprintf(ClusterRestService, ts.Name), ts.Namespace)
	return url.JoinPath(fmt.Sprintf("http://%s:%d", svc, ts.Spec.ApiPort), path)
}

type keysApiError struct {
	statusCode int
	message    string
	body       string
}

func (e *keysApiError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("typesense keys api returned %d: %s", e.statusCode, e.message)
	}
	return fmt.Sprintf("typesense keys api returned %d: %s", e.statusCode, e.body)
}

func isNotFoundErr(err error) bool {
	apiErr, ok := err.(*keysApiError)
	return ok && apiErr.statusCode == http.StatusNotFound
}
