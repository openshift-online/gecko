# Gecko — Agent Context

**GECKO** = GCP Engine for Cluster Kubernetes Orchestration. It is a managed-service control plane that brings HyperShift (OpenShift Hosted Control Planes) to Google Cloud Platform. Customers declare `Cluster` and `NodePool` objects via a Kubernetes-style API; Gecko's controllers place them on GKE management clusters, resolve release versions, and deliver manifests via Firestore + `kube-applier-gcp`.

---

## Repository layout

This is a **Go monorepo** with three independent Go modules connected by `replace` directives.

```
gecko/
├── orlop/           # Framework module: code generator + API server runtime
├── platform-api/    # API server module: Cluster, NodePool, Channel, Version types + binary
├── controllers/     # Controllers module: placement, hc, nodepool, versionresolution, …
├── hack/            # Shared Makefile fragments (lint.mk, …)
├── helm/            # Helm charts
├── deploy/          # Deploy manifests
└── .tekton/         # CI pipelines
```

Module paths:
- `github.com/openshift-online/gecko/orlop`
- `github.com/openshift-online/gecko/platform-api` (replaces `../orlop`)
- `github.com/openshift-online/gecko/controllers` (replaces `../platform-api` and `../orlop`)

---

## Orlop framework (`orlop/`)

Orlop is the framework that `platform-api` is built on. It provides two things:

### 1. `orlop-gen` — code generator (`orlop/cmd/orlop-gen/`)

Run from `platform-api/` as `make generate`. Reads `api/private/v1/` and writes `api/public/v1/`. Never edit `api/public/zz_generated.*` files by hand.

What it generates:
| Output | Location |
|---|---|
| Filtered public Go types | `api/public/v1/zz_generated.<file>.go` |
| DeepCopy methods | `zz_generated.deepcopy.go` (both private and public) |
| Embedded OpenAPI v3 schemas | `.schemas/<type>_schema.yaml` + `zz_generated.schemas.go` |
| Conversion functions | `zz_generated.conversion.go` |

Markers consumed by `orlop-gen`:
- `// +orlop:public` on the `groupversion_info.go` package doc → opt the whole package into generation
- `// +orlop:public` on a struct field → include that field in the generated public type
- `// +orlop:public-verbs: <verb>[,<verb>…]` on a **type doc comment** → restrict which HTTP verbs the public API exposes for that specific type; valid verbs: `create get list update patch delete watch`; absent = all verbs allowed
- Standard `+kubebuilder:*` markers are passed through to controller-gen for CRD generation, printer columns, and validation

The `+orlop:public-verbs` annotation lives on the type declaration — not the package doc, not a field:
```go
// +kubebuilder:object:root=true
// +orlop:public-verbs: list,get
type Channel struct { … }
```
Different types in the same package can have different verb sets.

### 2. API server runtime (`orlop/pkg/apiserver/`)

| Package | Purpose |
|---|---|
| `apiserver` | Top-level: `Server`, `Options`, `ResourceRegistry`, `StorageFactory` |
| `apiserver/types` | `ResourceInfo`, `ParentResourceInfo`, `PrinterColumn`, `VerbAllowed()` |
| `apiserver/aggregated` | `AggregatedServer` wrapping `GenericAPIServer` (private API) |
| `apiserver/handlers` | `ResourceHandler`, `ConvertingResourceHandler`, `DiscoveryHandler` |
| `apiserver/conversion` | `Converter` — bidirectional private ↔ public conversion |
| `apiserver/storage` | `ResourceStore` interface; MemoryStore, PostgresStore, SpannerStore backends |
| `apiserver/schema` | `Processor` — prune / default / validate via structural schema |
| `apiserver/gc` | `Collector` — owner-reference GC |
| `apiserver/apply` | `Manager` — server-side apply |
| `apiserver/middleware` | CORS |
| `generator` | All generation logic: `generator.go`, `schemas.go`, `conversions.go` |

---

## Dual-API architecture

| | Private API | Public API |
|---|---|---|
| Port | 8080 | 8081 |
| Protocol | HTTPS/TLS | Plain HTTP (ESPv2 in front in prod) |
| Server | Kubernetes `GenericAPIServer` | `chi.Router` |
| Auth | Delegated TokenReview + SubjectAccessReview | `X-Endpoint-API-UserInfo` header (base64url JWT) |
| Fields | All (including internal) | Only `+orlop:public`-marked fields |
| Status subresource | Yes | No (GCP-1062) |
| Verb restriction | Full Kubernetes RBAC | `+orlop:public-verbs` annotation |

Both APIs share the **same storage backend** via a memoizing `StorageFactory`. Objects are always stored in private type format.

### `ResourceInfo` — central resource descriptor

```go
type ResourceInfo struct {
    GVK            runtimeschema.GroupVersionKind
    Plural         string
    Singular       string
    Namespaced     bool
    SchemaYAML     string              // embedded OpenAPI v3 YAML
    ParentResource *ParentResourceInfo // for nested routing (e.g. NodePool under Cluster)
    PrinterColumns []PrinterColumn     // kubectl get columns
    Verbs          []string            // from +orlop:public-verbs; nil = all verbs allowed
}

func (r ResourceInfo) VerbAllowed(verb string) bool // true when Verbs is nil/empty or contains verb
```

Generated into `zz_generated.schemas.go`; retrieved via `GetResourceInfos()`.

### Converter (`conversion/conversion.go`)

`PrivateToPublic`:
- JSON round-trip (drops fields absent from public type)
- Strips `private.orlop.gcp.managed.openshift.io/`-prefixed labels, annotations, finalizers
- Filters conditions through `publicConditionTypes` allowlist

`PublicToPrivate`:
- Overlays public input onto existing private object (preserving internal fields)
- Reconciles labels/annotations (additive merge)
- Re-injects non-public conditions from the existing private object

`publicConditionTypes` (in `conversion.go`) — **add new public conditions here**:
```go
var publicConditionTypes = map[string]sets.Set[string]{
    "Cluster":  sets.New("HostedClusterAvailable"),
    "NodePool": sets.New("NodePoolAvailable", "NodePoolHealthy", "NodePoolProgressing"),
}
```

### Private prefix

`private.orlop.gcp.managed.openshift.io/` — any label, annotation, or condition type with this prefix is automatically stripped during `PrivateToPublic` conversion. Used for controller-internal bookkeeping that must never leak to public consumers.

---

## Platform-API types

API group: `gcp.managed.openshift.io/v1`

Source of truth: `platform-api/api/private/v1/` — **always edit private types, never public**.

| Kind | Scope | Description |
|---|---|---|
| `Cluster` | Namespaced | A managed Hosted Cluster on GCP |
| `NodePool` | Namespaced | Node pool for a Cluster; nested under `clusters/{clusterID}/nodepools` |
| `Channel` | Cluster-scoped | Platform-managed read-only; default version + fleet upgrade info |
| `Version` | Cluster-scoped | Platform-managed read-only; Cincinnati-synchronized release info |

Key `ClusterSpec` fields:
- **Public**: `infraID`, `issuerURL`, `platform` (GCP project, region, network, subnet, endpointAccess, workloadIdentity, resourceLabels), `release` (version, channelGroup), `networking`, `dns.baseDomain`
- **Private only**: `safeName` (max 17 chars; DNS-safe name for HyperShift resources; auto-generated from `name+uid` via `CustomDefaulter`)

`NodePool.spec.clusterID` links a NodePool to its parent Cluster. The nested route `/clusters/{clusterID}/nodepools` uses `ParentResourceInfo{Plural: "clusters", IDField: "spec.clusterID"}`.

---

## Controllers (`controllers/`)

Built on `controller-runtime`. All are sub-commands of a single `gecko-controllers` binary.

| Controller | Watches | Writes |
|---|---|---|
| `placement` | `Cluster` | `Status.PlacementResult` (managementClusterName, baseDomain) |
| `version-resolution` | `Cluster` | `Status.VersionResolution` (releaseImage, releaseVersion, …) |
| `nodepoolvrresolution` | `NodePool` | `Status.VersionResolution` |
| `hc-controller` | `Cluster` | HC conditions + hostedClusterResult |
| `nodepool-controller` | `NodePool` | NP conditions |
| `versionsync` | — (periodic) | `Version` resources |

**Transport layer** (`client/transport/`): The HC and NodePool controllers write `ApplyDesire` Firestore documents; `kube-applier-gcp` (external service) processes them and writes status feedback. A mock transport is used in tests.

**Requeue timings**: 15 s while waiting for dependencies; 5 m after successful reconciliation.

**Placement**: Discovers management clusters from GCP Secret Manager secrets labeled `mc-registration=true`. Round-robin assignment.

---

## Storage backends

Selected at startup by environment variables (priority order):

1. **Spanner** — `SPANNER_DATABASE=projects/<p>/instances/<i>/databases/<d>` is set
   - Optional: `SPANNER_TABLE_PREFIX`, `SPANNER_EMULATOR_HOST`, `SKIP_DDL_SETUP`
   - `--migrate-only` flag for ArgoCD PreSync Job DDL migrations
2. **PostgreSQL** — `DB_HOST` is set (`DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, `DB_SSLMODE`)
3. **Memory** — default; non-persistent; for local dev and unit tests

---

## Infrastructure (`gcp-hcp-infra`)

The GKE management clusters that gecko places hosted clusters onto are provisioned by the `gcp-hcp-infra` repo (Terraform + ArgoCD). Key concepts:

- **Global cluster** (1 per env): ArgoCD + External Secrets + GCS state
- **Region cluster** (1 per region per sector): Regional GKE with Fleet Config Sync
- **Management cluster** (many per region): GKE clusters hosting HyperShift operators

Naming conventions:
- Management cluster project: `{env}-mgt-{region_code}-{infra_id}` (e.g., `prd-mgt-us-c1-b5x9`)
- DNS zones for hosted clusters: `hc-{region}-{infra_id}-{N}`
- `infra_id`: 4-char lowercase alphanumeric starting with a letter

**After editing `argocd/config/`**, always run `uv run argocd/scripts/render.py` to regenerate `argocd/rendered/`. ArgoCD reads `rendered/`, not `config/`.

---

## Key workflows

### Adding or changing an API field

1. Edit `platform-api/api/private/v1/<type>_types.go`
2. Add `// +orlop:public` to the field comment if it should be visible on the public API
3. Add kubebuilder validation markers as needed
4. Run `make generate` in `platform-api/`
5. The generated `api/public/v1/` files update automatically — do not edit them

### Restricting public API verbs for a type

Add `// +orlop:public-verbs: <verbs>` to the type's doc comment in the **private** type file:
```go
// +kubebuilder:object:root=true
// +orlop:public-verbs: list,get
type Channel struct { … }
```
Then run `make generate`. The generated `ResourceInfo` will carry the `Verbs` field; the public router will return `501 Not Implemented` for unlisted verbs; the OpenAPI spec and discovery endpoint will omit them.

### Adding a new public condition

Edit `publicConditionTypes` in `orlop/pkg/apiserver/conversion/conversion.go`:
```go
"Cluster": sets.New("HostedClusterAvailable", "MyNewCondition"),
```

### Running the server locally

```bash
cd platform-api
make server-start   # memory backend, ports 18080/18081, no auth
# or manually:
go run ./cmd/platform-api-server --disable-auth
```

### Running tests

```bash
# All modules from repo root:
make test

# orlop only:
cd orlop && make test

# Skip DB tests:
cd orlop && make test-unit

# With postgres:
cd orlop && make test-postgres

# With spanner emulator:
cd orlop && make test-spanner
```

---

## Important invariants

- **No CRDs installed**: Orlop is an aggregated API server. The CRD YAML files produced by `controller-gen` during `make generate` are temporary intermediate artifacts parsed for schema/printer column extraction and immediately deleted. Never commit or install them.
- **Generated files**: Never manually edit files matching `zz_generated.*`.
- **Private prefix**: `private.orlop.gcp.managed.openshift.io/` — anything with this prefix is controller-internal and auto-stripped from the public API.
- **ESPv2 header**: In production, the public API receives `X-Endpoint-API-UserInfo` (base64url-encoded JSON with `"email"` claim) from ESPv2. Gecko stores this as a private annotation.
- **NodePool parent link**: `NodePool.spec.clusterID` must match the name of an existing Cluster in the same namespace.
- **`safeName`**: Auto-generated from `name+uid` (max 17 chars). Immutable after creation. Never set by users.
- **Server-side apply**: Enabled automatically when a structural schema builds successfully for a resource.
- **Status subresource**: Deliberately NOT registered on the public API (GCP-1062). Controllers write status via the private API only.

---

## Linting

Uses `golangci-lint` with `sigs.k8s.io/kube-api-linter` as a CGO plugin. Config in `.golangci.yml` per module. Run via:

```bash
make lint          # all modules
cd orlop && make lint
cd platform-api && make lint
cd controllers && make lint
```
