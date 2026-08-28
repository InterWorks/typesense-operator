# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

TyKO (Typesense Kubernetes Operator) — a Kubernetes operator, built with Operator SDK / kubebuilder (go.kubebuilder.io/v4), that manages the full lifecycle of highly-available Typesense clusters via a single CRD (`TypesenseCluster`, group `ts.opentelekomcloud.com/v1alpha1`). It automates ConfigMaps, Secrets, PVCs, StatefulSets, Services, Ingress/HTTPRoute, metrics scrapers, and — most notably — Raft quorum discovery/recovery without sidecars.

## Common commands

```bash
# Build & code hygiene
make manifests generate      # regenerate CRDs/RBAC (controller-gen) and deepcopy code — run after editing api/v1alpha1 types or +kubebuilder markers
make fmt vet                 # go fmt / go vet
make build                   # manifests generate fmt vet + go build -> bin/manager
make run                     # run the operator against the current kubeconfig context (out-of-cluster)

# Lint
make lint                    # golangci-lint run
make lint-fix                # golangci-lint run --fix

# Tests
make test                    # runs manifests generate fmt vet setup-envtest, then envtest-based unit/integration tests for all packages except test/
# Run a single package's tests directly once envtest assets are set up:
KUBEBUILDER_ASSETS="$(setup-envtest use -p path)" go test ./internal/controller/... -run TestSomething -v

make test-e2e                # spins up a local Kind cluster, runs test/e2e (ginkgo), tears the cluster down
                              # CERT_MANAGER_INSTALL_SKIP=true to skip cert-manager install

# CRDs / deployment (kustomize-based)
make install                 # install CRDs into the cluster in ~/.kube/config
make deploy                  # deploy the controller via config/default kustomize overlay
make deploy-with-samples      # install + apply sample CRs
make undeploy / uninstall    # tear down

# Helm chart (charts/typesense-operator) is generated from kustomize output — see `make helmify`
```

Tests use Ginkgo/Gomega (`internal/controller/suite_test.go`, `test/e2e`), driven through envtest for controller-level tests and a real Kind cluster for e2e.

## Architecture

### Single controller, many reconcile phases

There is one reconciler, `TypesenseClusterReconciler` (`internal/controller/typesensecluster_controller.go`), driving one CRD (`TypesenseCluster`). `Reconcile()` runs a fixed sequence of phases, each in its own file, each with its own idempotent update strategy (documented as a comment above the call site in `typesensecluster_controller.go`):

1. `ReconcileSecret` (`typesensecluster_secret.go`) — admin API key Secret; **immutable**, never updated after creation.
2. `ReconcileConfigMap` (`typesensecluster_configmap.go`) — the peer-nodes ConfigMap (`ClusterNodesConfigMap`); updated in place when the node list changes, and its return value (`configMapUpdated *bool`) drives whether the rest of reconcile treats this pass as bootstrap vs. a quorum-affecting change.
3. `ReconcileServices` (`typesensecluster_services.go`)
4. `ReconcileIngress` (`typesensecluster_ingress.go`)
5. `ReconcileHttpRoute` (`typesensecluster_httproute.go`) — Gateway API HTTPRoute, alternative to Ingress
6. `ReconcileScraper` (`typesensecluster_scraper.go`) — drops and recreates on change
7. `ReconcilePodMonitor` (`typesensecluster_podmonitor.go`) — Prometheus PodMonitor/metrics exporter
8. `ReconcileStatefulSet` (`typesensecluster_statefulset.go`) — the Typesense StatefulSet itself; full spec diff/update. `typesensecluster_statefulset_hash.go` computes a hash of the pod template to detect when a rolling update is actually needed.
9. `ReconcileQuorum` (`typesensecluster_quorum.go`, helpers in `typesensecluster_quorum_helpers.go`, types in `typesensecluster_quorum_types.go`) — talks to the Typesense HTTP health/stats API on each pod to compute Raft quorum health (available vs. min-required nodes, write/read lag), restarts unscheduled pods, and derives a `ConditionQuorum` status.

Each phase failure sets a "NotReady" status condition (`typesensecluster_condition_types.go`, `setConditionNotReady`/`setConditionReady`) and short-circuits the reconcile loop by returning early — phases are strictly sequential and later phases assume earlier ones succeeded.

### Bootstrapping vs. reconciling, and the ConfigMap-triggered requeue dance

After the StatefulSet phase, the controller distinguishes two actions based on whether `ReconcileConfigMap` reported a change:
- `configMapUpdated == nil`: nothing changed — either steady-state (`Reconciling`) or first-ever creation (`Bootstrapping`, short 15s requeue).
- `configMapUpdated != nil` and `true`: the peer list changed, so `forcePodsConfigMapUpdate` force-restarts pods to pick up the new mounted ConfigMap (kubelet syncs configmaps ~every 60s on its own), the condition is set to `QuorumNotReadyWaitATerm`, and reconcile requeues after `configMapRequeuePeriod` (2 min) to give kubelet time to propagate before checking quorum again.

Only once the ConfigMap has settled does the controller call `ReconcileQuorum` and fold its `ConditionQuorum` result into the CR's status/events (`QuorumNeedsAttention*` conditions surface as Warning events requiring manual intervention — lagging writes or out-of-memory/disk; anything else not-ready is retried).

### API types layout (`api/v1alpha1/`)

`typesensecluster_types.go` holds the root `TypesenseClusterSpec`/`Status`; the sub-structs for each concern live in their own `typesensecluster_types_*.go` files (`_storage`, `_service`, `_ingress`, `_httproute`, `_scraper`, `_metrics`, `_healthcheck`, `_securitycontexts`), with helper methods in `typesensecluster_types_helpers.go`. `zz_generated.deepcopy.go` is generated — never hand-edit it; run `make generate` instead.

### Config entry point

`cmd/main.go` wires up the manager: scheme registration, leader election, health/readiness probes, metrics server, and controller setup (`SetupWithManager`). It also builds the extra clients the reconciler needs beyond the controller-runtime client (`DiscoveryClient`, `ClientSet`, `InCluster` detection) since quorum health checks and pod restarts need direct API access.

## Making changes to the CRD

Any change to `api/v1alpha1/typesensecluster_types*.go` (new field, changed `+kubebuilder:` marker, etc.) requires `make manifests generate` before building/testing — this regenerates CRD YAML under `config/crd/` and `zz_generated.deepcopy.go`. The Helm chart under `charts/typesense-operator` is produced from the kustomize output via `make helmify` and should be regenerated alongside CRD/manifest changes, not edited by hand.

## golangci-lint notes

Config in `.golangci.yml`: `api/*` is exempt from `lll` (long lines — CRD marker comments run long); `internal/*` is exempt from `dupl` and `lll`. Keep new code lint-clean under the enabled linter set (see file) rather than adding new exemptions.
