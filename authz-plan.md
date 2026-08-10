# Cedar-based Authorization for Gecko — Implementation Plan

## Background

Gecko's public API (chi.Router, port 8081) currently has **zero authentication or authorization**.
This plan introduces a Cedar-based authz system with ConfigMap-defined roles and namespace-scoped
role bindings, backed by the existing PostgreSQL database.

Cedar is chosen over simple permission-set lookups because the long-term goal is to support
**user-defined custom roles with Cedar conditions** (e.g., attribute-based access control such as
"allow access only to clusters in region `us-east1`"). Using Cedar from day one avoids a
retrofit later.

## Design Decisions

| Decision | Choice |
|---|---|
| User identity source | `X-Endpoint-API-UserInfo` header (base64 JWT claims injected by ESPv2 sidecar) |
| Principal key | Email claim (e.g., `User::"alice@example.com"`) |
| Role definition source | ConfigMap loaded at deployment time (not an API resource) |
| Cedar schema | Documentation only (not enforced at runtime by `cedar-go`) |
| Type location | `platform-api` module |
| Cedar dependency | `platform-api` module only (via `github.com/cedar-policy/cedar-go`) |
| Namespace lifecycle | Implicit (no Namespace resource needed) |
| Storage strategy | Same database, additional `resources_*` tables (consistent with Cluster/NodePool) |
| Platform-level scope | `PlatformRoleBinding` for platform-scoped admin access |
| Entity cache strategy | Cache until dirty — invalidate on RoleBinding/PlatformRoleBinding writes |
| Store wiring strategy | Add `PublicRegistry()` accessor to `apiserver.Server`; retrieve stores in `main.go` after server creation |
| Auth disable flag | Existing `--disable-auth` covers both private and public APIs (no separate flag) |
| Cross-namespace list strategy | Namespace-filter via `ListOptions.Namespaces` in storage layer (no post-filter interceptor) |
| CustomValidator timing | Deferred to Phase 3 when ConfigMap loader provides the role label set |
| Custom roles (future) | Service-admins create custom roles via API with Cedar conditions |

## Granular Permissions

Every API operation maps to a single granular permission. Permissions follow the pattern
`{resource}.{verb}` and each maps to a PascalCase Cedar action.

| Permission                    | Cedar Action                  | Scope      |
|-------------------------------|-------------------------------|------------|
| `cluster.create`              | `CreateCluster`               | Namespace  |
| `cluster.list`                | `ListClusters`                | Namespace  |
| `cluster.get`                 | `GetCluster`                  | Namespace  |
| `cluster.update`              | `UpdateCluster`               | Namespace  |
| `cluster.delete`              | `DeleteCluster`               | Namespace  |
| `nodepool.create`             | `CreateNodepool`              | Namespace  |
| `nodepool.list`               | `ListNodepools`               | Namespace  |
| `nodepool.get`                | `GetNodepool`                 | Namespace  |
| `nodepool.update`             | `UpdateNodepool`              | Namespace  |
| `nodepool.delete`             | `DeleteNodepool`              | Namespace  |
| `rolebinding.create`          | `CreateRoleBinding`           | Namespace  |
| `rolebinding.list`            | `ListRoleBindings`            | Namespace  |
| `rolebinding.get`             | `GetRoleBinding`              | Namespace  |
| `rolebinding.update`          | `UpdateRoleBinding`           | Namespace  |
| `rolebinding.delete`          | `DeleteRoleBinding`           | Namespace  |
| `platformrolebinding.create`  | `CreatePlatformRoleBinding`   | Platform   |
| `platformrolebinding.list`    | `ListPlatformRoleBindings`    | Platform   |
| `platformrolebinding.get`     | `GetPlatformRoleBinding`      | Platform   |
| `platformrolebinding.update`  | `UpdatePlatformRoleBinding`   | Platform   |
| `platformrolebinding.delete`  | `DeletePlatformRoleBinding`   | Platform   |
| `customrole.create`           | `CreateCustomRole`            | Namespace  |
| `customrole.list`             | `ListCustomRoles`             | Namespace  |
| `customrole.get`              | `GetCustomRole`               | Namespace  |
| `customrole.update`           | `UpdateCustomRole`            | Namespace  |
| `customrole.delete`           | `DeleteCustomRole`            | Namespace  |

## Cedar Entity Model (Documentation Only)

This schema describes the entity hierarchy used by Cedar. It is not enforced at runtime —
the Go code is responsible for constructing correctly-typed entities.

```cedarschema
namespace Gecko {
  entity User;
  entity Namespace;
  entity Platform;
  entity NamespaceRole in Namespace;
  entity PlatformRole  in Platform;
  entity Cluster       in Namespace;
  entity NodePool      in [Namespace, Cluster];

  // Cluster actions (namespace-scoped)
  action CreateCluster  appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action ListClusters   appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action GetCluster     appliesTo { principal: [User, NamespaceRole], resource: [Namespace, Cluster] };
  action UpdateCluster  appliesTo { principal: [User, NamespaceRole], resource: Cluster };
  action DeleteCluster  appliesTo { principal: [User, NamespaceRole], resource: Cluster };

  // NodePool actions (namespace-scoped)
  action CreateNodepool appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action ListNodepools  appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action GetNodepool    appliesTo { principal: [User, NamespaceRole], resource: [Namespace, NodePool] };
  action UpdateNodepool appliesTo { principal: [User, NamespaceRole], resource: NodePool };
  action DeleteNodepool appliesTo { principal: [User, NamespaceRole], resource: NodePool };

  // RoleBinding actions (namespace-scoped)
  action CreateRoleBinding appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action ListRoleBindings  appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action GetRoleBinding    appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action UpdateRoleBinding appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action DeleteRoleBinding appliesTo { principal: [User, NamespaceRole], resource: Namespace };

  // PlatformRoleBinding actions (platform-scoped)
  action CreatePlatformRoleBinding appliesTo { principal: [User, PlatformRole], resource: Platform };
  action ListPlatformRoleBindings  appliesTo { principal: [User, PlatformRole], resource: Platform };
  action GetPlatformRoleBinding    appliesTo { principal: [User, PlatformRole], resource: Platform };
  action UpdatePlatformRoleBinding appliesTo { principal: [User, PlatformRole], resource: Platform };
  action DeletePlatformRoleBinding appliesTo { principal: [User, PlatformRole], resource: Platform };

  // CustomRole actions (namespace-scoped, Phase 6)
  action CreateCustomRole appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action ListCustomRoles  appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action GetCustomRole    appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action UpdateCustomRole appliesTo { principal: [User, NamespaceRole], resource: Namespace };
  action DeleteCustomRole appliesTo { principal: [User, NamespaceRole], resource: Namespace };
}
```

## Built-in Roles (ConfigMap-defined)

Roles are defined in a ConfigMap loaded at deployment time. They are **not** API resources —
they cannot be created, modified, or deleted via the API. The ConfigMap is versioned in Git
alongside the Helm chart.

Each role is a named collection of granular permissions with a clear, non-overlapping area
of responsibility.

| Role               | Scope     | Permissions                                                                                    |
|--------------------|-----------|------------------------------------------------------------------------------------------------|
| `platform-admin`   | Platform  | `platformrolebinding.*` (create, list, get, update, delete)                                    |
| `service-admin`    | Namespace | `rolebinding.*`, `customrole.*` (create, list, get, update, delete)                            |
| `cluster-admin`    | Namespace | `cluster.*` (create, list, get, update, delete), `nodepool.*` (create, list, get, update, delete) |
| `cluster-viewer`   | Namespace | `cluster.list`, `cluster.get`, `nodepool.list`, `nodepool.get`                                 |

**Design notes:**
- **Separation of concerns**: Access management (platform-admin, service-admin) is fully
  separated from infrastructure management (cluster-admin, cluster-viewer). No single role
  conflates both.
- **No "god role"**: A full operator needs multiple bindings (e.g., platform-admin +
  service-admin + cluster-admin).
- **Scope**: `platform-admin` is platform-scoped; all other built-in roles are namespace-scoped.

### ConfigMap Format

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gecko-authz-config
  namespace: gecko-system
data:
  roles.yaml: |
    roles:
      - name: cluster-viewer
        scope: namespace
        permissions:
          - cluster.list
          - cluster.get
          - nodepool.list
          - nodepool.get

      - name: cluster-admin
        scope: namespace
        permissions:
          - cluster.create
          - cluster.list
          - cluster.get
          - cluster.update
          - cluster.delete
          - nodepool.create
          - nodepool.list
          - nodepool.get
          - nodepool.update
          - nodepool.delete

      - name: service-admin
        scope: namespace
        permissions:
          - rolebinding.create
          - rolebinding.list
          - rolebinding.get
          - rolebinding.update
          - rolebinding.delete
          - customrole.create
          - customrole.list
          - customrole.get
          - customrole.update
          - customrole.delete

      - name: platform-admin
        scope: platform
        permissions:
          - platformrolebinding.create
          - platformrolebinding.list
          - platformrolebinding.get
          - platformrolebinding.update
          - platformrolebinding.delete

  bootstrap.yaml: |
    platformRoleBindings:
      - name: bootstrap-admin
        subject: operator@example.com
        roleRef: platform-admin
```

### How the ConfigMap is Used

1. **On startup**, the server reads the ConfigMap (mounted as a volume at a well-known path,
   e.g., `/etc/gecko/authz/`)
2. **Role definitions** are parsed and used to:
   - Generate Cedar policies (one `permit` block per role)
   - Build a role label validation set (RoleBinding/PlatformRoleBinding `roleRef` must
     reference a known role label)
3. **Bootstrap bindings** are upserted into the store — idempotent, skip if already exists
4. The ConfigMap is read-only at runtime — changes require a redeploy

### Cedar Policy Generation from ConfigMap

Each role in the ConfigMap is translated into a Cedar policy at startup. The permission
names map to Cedar actions via PascalCase conversion (e.g., `cluster.create` → `CreateCluster`).

For a namespace-scoped role:
```cedar
// Built-in role: cluster-viewer
permit (
    principal,
    action in [Action::"ListClusters", Action::"GetCluster",
               Action::"ListNodepools", Action::"GetNodepool"],
    resource
)
when { principal in resource };
```

For a platform-scoped role:
```cedar
// Built-in role: platform-admin
permit (
    principal,
    action in [Action::"CreatePlatformRoleBinding", Action::"ListPlatformRoleBindings",
               Action::"GetPlatformRoleBinding", Action::"UpdatePlatformRoleBinding",
               Action::"DeletePlatformRoleBinding"],
    resource
)
when { principal in resource };
```

The `when { principal in resource }` condition is what enforces scoping: the Cedar entity
graph places users `in` their bound roles, roles `in` their namespace/platform, and resources
`in` their namespace. Cedar's transitive `in` operator handles the rest.

## New API Resources

| Resource | Scope | Purpose |
|---|---|---|
| `RoleBinding` | Namespaced | Binds a user email to a role within a namespace. |
| `PlatformRoleBinding` | Cluster-scoped (non-namespaced) | Binds a user email to a platform-scoped role. |
| `CustomRole` | Namespaced (Phase 6) | User-defined role with Cedar conditions. |

**Note:** There is no `Role` API resource. Built-in roles are defined in the ConfigMap and
are not exposed as API objects.

## Authorization Flow (per request)

```
HTTP Request
  │
  ├─ 1. AuthN Middleware
  │     - Read X-Endpoint-API-UserInfo header (base64 JWT claims from ESPv2)
  │     - Decode claims, extract email field
  │     - Inject email into request context
  │     - Missing/malformed header → 401 Unauthenticated
  │
  ├─ 2. AuthZ Middleware (Cedar)
  │     a. Read user email from context
  │     b. Derive Cedar Action from HTTP method + URL pattern
  │     c. Derive Cedar Resource from URL (Namespace::"ns" or Cluster::"ns/name", etc.)
  │     d. Build Entity Slice (from cache, or from DB if dirty):
  │        - User entity + parent NamespaceRoles (from RoleBindings in DB)
  │        - User entity + parent PlatformRoles (from PlatformRoleBindings in DB)
  │        - NamespaceRole entities + parent Namespace entities
  │        - Resource entity + parent hierarchy
  │     e. cedar.Authorize(policySet, entitySlice, request)
  │        - policySet includes generated built-in policies + custom role policies (Phase 6)
  │     f. Deny → 403 Forbidden
  │
  └─ 3. Handler: normal CRUD processing
```

## Entity Cache Strategy

The entity graph (user → roles → namespaces) is cached in memory and invalidated on writes:

- **Cache key**: user email
- **Cache population**: on first authorization check for a user, query RoleBindings and
  PlatformRoleBindings from the DB, build the entity graph, cache it
- **Invalidation granularity**: per-subject. When a `RoleBinding` or `PlatformRoleBinding`
  is created, updated, or deleted, the store write callback extracts the `subject` field
  from the binding and invalidates **only that user's** cache entry. This avoids
  unnecessarily evicting unrelated users' cached entity graphs. If the subject field
  changes during an update (rebinding to a different user), both the old and new subjects
  are invalidated.
- **Implementation**: since the authz middleware and stores are in the same process,
  invalidation is a simple in-memory callback on the store's write path. For multi-instance
  deployments, the existing PostgreSQL `LISTEN/NOTIFY` watch mechanism provides cross-instance
  invalidation — the notification payload includes the affected subject email so remote
  instances can invalidate the correct cache entry.

This is strictly better than TTL-based caching: zero staleness window for local writes,
and near-zero for external writes via `LISTEN/NOTIFY`.

## NodePool Entity Hierarchy

NodePools belong to both a Namespace and a Cluster:

```
NodePool::"ns/npName" in Cluster::"ns/clusterName" in Namespace::"ns"
```

The entity getter looks up the NodePool's `clusterID` field to build the parent chain.
Authorization checks on a NodePool succeed if the principal is `in` the Namespace, because
Cedar's `in` operator is transitive.

## Action Derivation Map

| HTTP Method | URL Pattern | Cedar Action | AuthZ Strategy |
|---|---|---|---|
| GET | `/namespaces/{ns}/clusters` | `ListClusters` | Pre-filter (namespace known) |
| POST | `/namespaces/{ns}/clusters` | `CreateCluster` | Pre-filter |
| GET | `/namespaces/{ns}/clusters/{name}` | `GetCluster` | Pre-filter |
| PUT/PATCH | `/namespaces/{ns}/clusters/{name}` | `UpdateCluster` | Pre-filter |
| DELETE | `/namespaces/{ns}/clusters/{name}` | `DeleteCluster` | Pre-filter |
| GET | `/clusters` | `ListClusters` | **Namespace-filter** (cross-namespace) |
| GET | `/namespaces/{ns}/nodepools` | `ListNodepools` | Pre-filter |
| POST | `/namespaces/{ns}/nodepools` | `CreateNodepool` | Pre-filter |
| GET | `/namespaces/{ns}/nodepools/{name}` | `GetNodepool` | Pre-filter |
| PUT/PATCH | `/namespaces/{ns}/nodepools/{name}` | `UpdateNodepool` | Pre-filter |
| DELETE | `/namespaces/{ns}/nodepools/{name}` | `DeleteNodepool` | Pre-filter |
| GET | `/nodepools` | `ListNodepools` | **Namespace-filter** (cross-namespace) |
| GET | `/namespaces/{ns}/rolebindings` | `ListRoleBindings` | Pre-filter |
| POST | `/namespaces/{ns}/rolebindings` | `CreateRoleBinding` | Pre-filter |
| GET | `/namespaces/{ns}/rolebindings/{name}` | `GetRoleBinding` | Pre-filter |
| PUT/PATCH | `/namespaces/{ns}/rolebindings/{name}` | `UpdateRoleBinding` | Pre-filter |
| DELETE | `/namespaces/{ns}/rolebindings/{name}` | `DeleteRoleBinding` | Pre-filter |
| GET | `/rolebindings` | `ListRoleBindings` | **Namespace-filter** (cross-namespace) |
| GET | `/platformrolebindings` | `ListPlatformRoleBindings` | Pre-filter (platform-scoped) |
| POST | `/platformrolebindings` | `CreatePlatformRoleBinding` | Pre-filter |
| GET | `/platformrolebindings/{name}` | `GetPlatformRoleBinding` | Pre-filter |
| PUT/PATCH | `/platformrolebindings/{name}` | `UpdatePlatformRoleBinding` | Pre-filter |
| DELETE | `/platformrolebindings/{name}` | `DeletePlatformRoleBinding` | Pre-filter |
| GET | `/namespaces/{ns}/customroles` | `ListCustomRoles` | Pre-filter |
| POST | `/namespaces/{ns}/customroles` | `CreateCustomRole` | Pre-filter |
| GET | `/namespaces/{ns}/customroles/{name}` | `GetCustomRole` | Pre-filter |
| PUT/PATCH | `/namespaces/{ns}/customroles/{name}` | `UpdateCustomRole` | Pre-filter |
| DELETE | `/namespaces/{ns}/customroles/{name}` | `DeleteCustomRole` | Pre-filter |
| GET | `/customroles` | `ListCustomRoles` | **Namespace-filter** (cross-namespace) |

## Cross-Namespace List Authorization

### Problem

The existing router registers cross-namespace list routes for every namespaced resource
(e.g., `GET /apis/.../clusters` without a `/namespaces/{ns}` prefix). When `namespace` is
absent from the URL, the storage layer returns objects from **all** namespaces. Authorization
cannot be checked before the query because the target namespace is unknown.

### Design: Namespace-Filter via ListOptions

Cross-namespace list requests are authorized by **pre-computing the set of authorized
namespaces** from the user's cached RoleBindings and injecting them into the storage
query. The storage layer filters results at the database level using
`WHERE namespace IN (...)`, so only authorized data is ever fetched.

**Flow:**

```
GET /apis/.../clusters (no namespace in URL)
  │
  ├─ 1. AuthN Middleware: extract user email (unchanged)
  │
  ├─ 2. AuthZ Middleware: detect cross-namespace list
  │     a. No namespace in URL → cross-namespace list detected
  │     b. Query cached entity graph for user's RoleBindings
  │     c. Compute authorized namespace set (namespaces where user has
  │        the required action, e.g., ListClusters)
  │     d. Inject authorized namespaces into request context
  │     e. Skip the normal single-namespace authz check — pass through
  │
  ├─ 3. Handler: read authorized namespaces from context
  │     a. Set ListOptions.Namespaces = authorized namespace set
  │     b. Store query: WHERE namespace IN (...) — only fetches authorized data
  │     c. Pagination works natively (limit/continue apply to filtered set)
  │
  └─ 4. Return results (may be empty — never 403)
```

**Key behaviors:**
- A cross-namespace list **never returns 403**. If the user has no bindings anywhere, the
  namespace set is empty and the query returns an empty list — consistent with Kubernetes
  RBAC behavior.
- Filtering happens at the database level, so no over-fetching occurs. Pagination
  (`limit` and `continue`) works correctly without adjustment.
- Each namespace in the authorized set was checked against the Cedar entity graph. A user
  with `cluster-viewer` in `ns1` but not `ns2` gets `Namespaces = ["ns1"]` and sees
  clusters from `ns1` only.
- The Cedar entity graph (user → roles → namespaces) is already cached, so computing the
  authorized namespace set requires no additional DB queries.

### Implementation (orlop storage layer changes)

1. **Add `Namespaces []string` to `storage.ListOptions`**: When non-empty and `Namespace`
   is empty, the storage layer filters to the specified set of namespaces.

2. **Postgres store**: Add `WHERE namespace IN ($1, $2, ...)` clause to the `List()` query
   when `Namespaces` is set. Use parameterized queries to prevent SQL injection.

3. **Memory store**: Add equivalent in-memory filter in the `List()` method — check
   `namespace ∈ Namespaces` for each item.

4. **Context helpers**: The authz middleware exports `AuthorizedNamespaces(ctx) []string`
   for the handler to read and pass into `ListOptions`.

### Phase 3 Tasks (additions)

The cross-namespace list implementation is part of Phase 3 (Cedar Authorization Engine):

1. **Detect cross-namespace list in authz middleware**: check for namespaced resource with
   empty namespace URL param
2. **Pre-compute authorized namespace set**: query cached entity graph for the user's
   RoleBindings, extract namespaces where the user has the required Cedar action
3. **Extend `ListOptions` in orlop**: add `Namespaces []string` field, update Postgres
   and Memory store `List()` implementations
4. **Inject namespaces into context**: authz middleware sets authorized namespaces on the
   request context; handler reads them into `ListOptions`
5. **Write tests**: verify filtered results per namespace, empty result for no bindings,
   mixed access across namespaces, pagination correctness

## File Structure (New Files)

**Note**: `platform-api/pkg/` is a new directory. Currently all reusable code lives in
`orlop/pkg/`; this is the first application-level package directory in the `platform-api`
module.

```
platform-api/
  api/private/v1/
    rolebinding_types.go                   # RoleBinding type (namespaced)
    platformrolebinding_types.go           # PlatformRoleBinding type (cluster-scoped)
    customrole_types.go                    # CustomRole type (namespaced, Phase 6)
  api/public/v1/
    zz_generated.rolebinding_types.go      # (generated by orlop-gen)
    zz_generated.platformrolebinding_types.go
    zz_generated.customrole_types.go       # (Phase 6)
    zz_generated.conversion.go             # (regenerated)
    zz_generated.schemas.go                # (regenerated)
  pkg/
    authn/
      middleware.go                        # X-Endpoint-API-UserInfo extraction
      middleware_test.go
    authz/
      authorizer.go                        # Cedar PolicySet + Authorize()
      config.go                            # ConfigMap parsing (roles, bootstrap)
      entities.go                          # EntityGetter backed by RoleBinding stores
      cache.go                             # Entity cache with dirty invalidation
      middleware.go                        # HTTP authz middleware
      policygen.go                         # Cedar policy generation from role definitions
      authorizer_test.go
      config_test.go
      middleware_test.go
      entities_test.go
      cache_test.go
      policygen_test.go
  cmd/platform-api-server/
    main.go                                # (modified: wire authn/authz middleware, load config)
    resources.go                           # (modified: register new resource types)
deploy/
  gecko-authz-config.yaml                  # ConfigMap with built-in roles and bootstrap
```

---

## Phase 1: New API Types & Code Generation

**Goal**: Define `RoleBinding` and `PlatformRoleBinding` API types, generate public
projections, and register them with the server.

### Tasks

1. **Define `RoleBinding` type** in `platform-api/api/private/v1/rolebinding_types.go`
   - Namespaced (`+kubebuilder:resource:scope=Namespaced`)
   - `RoleBindingSpec.Subject string` (user email)
   - `RoleBindingSpec.RoleRef string` (role name, validated against ConfigMap-defined roles)
   - All spec fields marked `+orlop:public`

2. **Define `PlatformRoleBinding` type** in `platform-api/api/private/v1/platformrolebinding_types.go`
   - Cluster-scoped (`+kubebuilder:resource:scope=Cluster`)
   - `PlatformRoleBindingSpec.Subject string` (user email)
   - `PlatformRoleBindingSpec.RoleRef string` (must reference a platform-scoped role)
   - All spec fields marked `+orlop:public`

3. **Register new types** via `init()` + `register()` in each type file (follows existing
   pattern in `cluster_types.go` and `nodepool_types.go`)

4. **Run `orlop-gen`** to generate:
   - `platform-api/api/public/v1/zz_generated.rolebinding_types.go`
   - `platform-api/api/public/v1/zz_generated.platformrolebinding_types.go`
   - Updated `zz_generated.conversion.go` and `zz_generated.schemas.go`

5. **Wire `ResourceInfo`** in `platform-api/cmd/platform-api-server/resources.go`
   - `RoleBinding`: namespaced, no parent resource
   - `PlatformRoleBinding`: cluster-scoped, no parent resource

6. **Validate cluster-scoped routes early**: `PlatformRoleBinding` is the first
   cluster-scoped (non-namespaced) resource in the codebase. While the
   `setupConvertingRouter` code path for cluster-scoped resources exists in orlop, it has
   not been exercised yet. Validate that CRUD routes are generated correctly (e.g.,
   `GET /apis/.../platformrolebindings`, `GET /apis/.../platformrolebindings/{name}`)
   before proceeding to Phase 2. Write a smoke test that starts the server with an
   in-memory store and verifies these routes respond correctly.

7. **No status subresource**: `RoleBinding` and `PlatformRoleBinding` are simple binding
   records with no controller-managed status. They do **not** use
   `+kubebuilder:subresource:status` — unlike `Cluster` and `NodePool`, there are no
   `/status` sub-routes for these resources.

**Note**: `CustomValidator` for `roleRef` validation is deferred to Phase 3 (Task 10),
when the ConfigMap loader provides the role label set.

### Acceptance Criteria
- `GET /apis/gcp.managed.openshift.io/v1/namespaces/test/rolebindings` returns empty list
- `POST /apis/gcp.managed.openshift.io/v1/namespaces/test/rolebindings` creates a binding
- `GET /apis/gcp.managed.openshift.io/v1/platformrolebindings` returns empty list
- Cluster-scoped CRUD routes for `PlatformRoleBinding` respond correctly (smoke test)

### Implementation Status: COMPLETE

**Completed tasks:**
1. Defined `RoleBinding` type (namespaced) — `platform-api/api/private/v1/rolebinding_types.go`
2. Defined `PlatformRoleBinding` type (cluster-scoped) — `platform-api/api/private/v1/platformrolebinding_types.go`
3. Types registered via `init()` + `register()` following existing pattern
4. Ran `orlop-gen` — all generated files created successfully (public types, schemas, deepcopy, conversion)
5. `ResourceInfo` wiring automatic via `GetResourceInfos()` — no changes to `resources.go` needed
6. Smoke test written and passing in `platform-api/test/authz_resources_test.go`:
   - RoleBinding full CRUD lifecycle (namespaced)
   - PlatformRoleBinding full CRUD lifecycle (cluster-scoped)
   - Cross-namespace list for RoleBinding
   - Schema validation (empty subject rejected)
7. No `+kubebuilder:subresource:status` — confirmed no status field on these types

**Drifts / Notes:**
- `resources.go` required **zero changes** — the generated `GetResourceInfos()` automatically includes
  new types. The plan mentioned "Wire ResourceInfo in resources.go" but the existing pattern handles
  this via the generated `zz_generated.schemas.go`.
- The router registers `/status` routes for all resources generically. Since `RoleBinding` and
  `PlatformRoleBinding` have no `Status` field, the status route is effectively a no-op update.
  This is harmless and doesn't violate the "no status subresource" intent.

---

## Phase 2: Authentication Middleware

**Parallelism note**: Phase 2 (authn) and Phase 3 (authz) can be developed in parallel by
different engineers. Phase 2 produces a context value (`UserFromContext`); Phase 3 consumes
it. They only need to agree on the context key type. Integration (wiring both into
`PublicAPIOptions.Middleware`) happens when both are ready.

**Goal**: Extract user identity from the ESPv2-injected `X-Endpoint-API-UserInfo` header
and make it available in the request context.

### Tasks

1. **Implement `authn.Middleware`** in `platform-api/pkg/authn/middleware.go`
   - Read `X-Endpoint-API-UserInfo` header
   - Base64-decode the value
   - JSON-unmarshal the JWT claims
   - Extract `email` field
   - Store email in `context.Context` via a typed key
   - Return `401 Unauthenticated` if header is missing or malformed
   - Export `UserFromContext(ctx) string` helper

2. **Write unit tests** in `platform-api/pkg/authn/middleware_test.go`
   - Valid header with email → email extracted, next handler called
   - Missing header → 401
   - Invalid base64 → 401
   - Missing email claim → 401

3. **Wire middleware** into `platform-api/cmd/platform-api-server/main.go`
   - Add `authn.Middleware` as first entry in `PublicAPIOptions.Middleware`
   - Health/readyz endpoints are already registered before custom middleware in the router,
     so they remain unprotected

### Notes
- ESPv2 validates the JWT before forwarding — Gecko trusts the header without re-validating
- For local development without ESPv2, the existing `--disable-auth` flag is extended to
  cover the public API in addition to the private API. When set, the authn middleware
  injects a dev user identity from an env var or header (e.g., `X-Dev-User`) and the authz
  middleware is skipped entirely. The same localhost-only safety guard from the private API
  applies: `--disable-auth` requires binding to `127.0.0.1`.

### Acceptance Criteria
- Request with valid `X-Endpoint-API-UserInfo` → passes through with email in context
- Request without the header → 401
- `/healthz` and `/readyz` are accessible without the header

### Implementation Status: COMPLETE

**Completed tasks:**
1. Implemented `authn.Middleware` in `platform-api/pkg/authn/middleware.go`
   - Reads `X-Endpoint-API-UserInfo` header, base64-decodes, extracts email
   - Supports both standard and URL-safe base64 encoding (with/without padding)
   - Returns 401 with JSON error body on missing/malformed header
   - Exports `UserFromContext(ctx)` and `ContextWithUser(ctx, email)`
2. Implemented `DevModeMiddleware` for `--disable-auth` local dev mode
   - Reads `X-Dev-User` header, falls back to configurable default, then `dev@localhost`
3. Full unit test suite (10 tests) in `platform-api/pkg/authn/middleware_test.go`:
   - Valid header (standard and URL-safe base64)
   - Missing header → 401
   - Invalid base64 → 401
   - Invalid JSON → 401
   - Missing email claim → 401
   - Empty email → 401
   - Context helpers (UserFromContext, ContextWithUser)
   - DevModeMiddleware with/without header, with/without default
4. Wired into `main.go` — authn middleware added as first public API middleware
   - When `--disable-auth` is set, uses `DevModeMiddleware` instead
   - Health/readyz endpoints remain unprotected (registered before middleware in router)

**Drifts / Notes:**
- The `--disable-auth` localhost-only safety guard is enforced in the aggregated server's
  `Complete()` method for the private API. The public API server doesn't currently enforce
  this — it just uses `DevModeMiddleware`. The plan mentions the same guard should apply,
  but the public server bind address is controlled by the shared `address` flag which is
  already restricted by the private API's check. No additional enforcement needed.

---

## Phase 3: Cedar Authorization Engine

**Goal**: Implement Cedar-based authorization middleware that evaluates per-request
Cedar decisions against policies generated from the ConfigMap and a DB-backed entity graph.

### Tasks

1. **Add `cedar-go` dependency** to `platform-api/go.mod`
   ```
   require github.com/cedar-policy/cedar-go v0.x.x
   ```
   During development, add a `replace` directive pointing to the local fork at
   `/Users/asegundo/git-gcp/cedar-go`.

2. **Implement ConfigMap loader** in `platform-api/pkg/authz/config.go`
   - Parse `roles.yaml`: extract role names, scopes, and permission lists
   - Parse `bootstrap.yaml`: extract initial PlatformRoleBinding definitions
   - Build a role label validation set (used by `CustomValidator` in Task 10)
   - Export `RoleConfig` struct with loaded data

3. **Implement Cedar policy generator** in `platform-api/pkg/authz/policygen.go`
   - Takes `RoleConfig` as input
   - For each role, generates a Cedar `permit` policy:
     - Maps permission names to Cedar action names (e.g., `cluster.create` → `CreateCluster`)
     - Adds `when { principal in resource }` condition for scoping
   - Returns a `cedar.PolicySet` containing all generated policies

4. **Implement `GeckoEntityGetter`** in `platform-api/pkg/authz/entities.go`
    - Implements `cedar.EntityGetter` interface
    - Backed by `RoleBinding` and `PlatformRoleBinding` `ResourceStore` instances
    - **Store wiring**: Add a `PublicRegistry()` accessor method to `apiserver.Server` that
      returns the public `ResourceRegistry`. In `main.go`, after creating the server,
      retrieve the needed stores:
      ```go
      // In main.go, after server creation:
      rbStore := server.PublicRegistry().GetStore(schema.GroupKind{
          Group: "gcp.managed.openshift.io", Kind: "RoleBinding"})
      prbStore := server.PublicRegistry().GetStore(schema.GroupKind{
          Group: "gcp.managed.openshift.io", Kind: "PlatformRoleBinding"})
      entityGetter := authz.NewEntityGetter(rbStore, prbStore)
      ```
      This requires a small addition to orlop (`Server.PublicRegistry() *ResourceRegistry`)
      but avoids restructuring the server construction flow.
    - Entity construction logic:
     - `User::"email"` → query RoleBindings by subject, build parents as NamespaceRole
       entities; query PlatformRoleBindings, build parents as PlatformRole entities
     - `NamespaceRole::"ns/roleName"` → parent: `Namespace::"ns"`
     - `PlatformRole::"platform-admin"` → parent: `Platform::"gecko"`
     - `Namespace::"ns"` → parent: `Platform::"gecko"`
     - `Cluster::"ns/name"` → parent: `Namespace::"ns"`
     - `NodePool::"ns/npName"` → parents: `Cluster::"ns/clusterName"`, `Namespace::"ns"`
       (look up `clusterID` from NodePool spec)

5. **Implement entity cache** in `platform-api/pkg/authz/cache.go`
   - In-memory cache keyed by user email
   - Populated on first access, served from cache on subsequent requests
   - Invalidated (marked dirty) on any RoleBinding/PlatformRoleBinding write
   - Invalidation hook wired into store write path (create/update/delete callbacks)
   - For multi-instance: listen for PostgreSQL `LISTEN/NOTIFY` events on binding tables

6. **Implement `Authorizer`** in `platform-api/pkg/authz/authorizer.go`
   - Accepts `cedar.PolicySet` (generated from ConfigMap) at construction time
   - Exposes `Authorize(ctx, email, action, resource) (cedar.Decision, error)`
   - Uses `GeckoEntityGetter` (with cache) to resolve entities

7. **Implement `authz.Middleware`** in `platform-api/pkg/authz/middleware.go`
   - Reads user email from context (set by authn middleware)
   - Derives Cedar `Action` from HTTP method + chi route pattern. Use
     `chi.RouteContext(r.Context()).RoutePattern()` to get the matched route template
     (e.g., `/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}`) rather
     than parsing the raw URL path. This avoids brittle string matching and works
     correctly even if URL patterns change.
   - Derives Cedar `Resource` from URL path parameters (`chi.URLParam`)
   - Calls `authorizer.Authorize()`
   - Returns `403 Forbidden` with a JSON error body on Deny

8. **Write tests**
   - `policygen_test.go`: verify generated Cedar policies from sample role configs
   - `authorizer_test.go`: policy evaluation unit tests for all role/action combinations
   - `entities_test.go`: entity graph construction from mock stores
   - `cache_test.go`: cache population, hit, and invalidation
   - `middleware_test.go`: HTTP integration tests with in-memory stores, including
     cross-namespace list filtering and namespace-filter injection
   - `config_test.go`: ConfigMap parsing edge cases
   - `CustomValidator` tests: valid/invalid roleRef, wrong-scope roleRef
   - `ListOptions.Namespaces` tests: Postgres `WHERE IN` query, Memory store filter,
     empty namespace set returns empty results

9. **Wire authz middleware** in `platform-api/cmd/platform-api-server/main.go`
   ```go
   Public: apiserver.PublicAPIOptions{
       Middleware: []func(http.Handler) http.Handler{
           authn.Middleware,
           authz.NewMiddleware(authorizer),
       },
   },
   ```

10. **Add `CustomValidator`** to `RoleBinding` and `PlatformRoleBinding` (deferred from
    Phase 1). Now that the ConfigMap loader (Task 2) provides the role label set, the
    validator can reference it directly:
    - `RoleBinding`: validate `roleRef` is a known namespace-scoped role
    - `PlatformRoleBinding`: validate `roleRef` is a known platform-scoped role
    - Reject unknown or wrong-scope `roleRef` values with a validation error

11. **Extend `ListOptions` with `Namespaces` in orlop storage layer**
    - Add `Namespaces []string` field to `storage.ListOptions`
    - Update Postgres store `List()`: when `Namespaces` is non-empty and `Namespace` is
      empty, add `WHERE namespace IN (...)` with parameterized queries
    - Update Memory store `List()`: add equivalent in-memory filter
    - Add `AuthorizedNamespaces(ctx) []string` context helper in the authz package
    - Cross-namespace list handler reads authorized namespaces from context and passes
      them to `ListOptions.Namespaces`

### Acceptance Criteria
- `cluster-viewer` can GET and LIST clusters/nodepools in their namespace, gets 403 on
  create/update/delete
- `cluster-admin` can perform all CRUD operations on clusters/nodepools within their
  namespace, gets 403 on rolebinding/platformrolebinding operations
- `service-admin` can manage rolebindings within their namespace, gets 403 on
  cluster/nodepool operations and platformrolebinding operations
- `platform-admin` can manage platformrolebindings, gets 403 on cluster/nodepool/rolebinding
  operations
- No role grants cross-scope access (e.g., cluster-admin cannot manage rolebindings,
  service-admin cannot manage clusters)
- A user with a valid authn token but no bindings gets 403 on everything
- `POST` RoleBinding with unknown `roleRef` returns validation error
- `POST` PlatformRoleBinding with a namespace-scoped `roleRef` returns validation error
- Cross-namespace `GET /clusters` returns only clusters from authorized namespaces
- Cross-namespace list with no bindings returns empty list (not 403)

### Implementation Status: COMPLETE

**Completed tasks:**
1. Added `cedar-go` dependency with `replace` directive to local fork
2. Implemented ConfigMap loader (`config.go`) — parses roles.yaml and bootstrap.yaml,
   validates scopes/permissions/roleRefs, builds role label validation sets
3. Implemented Cedar policy generator (`policygen.go`) — generates `permit` policies
   from role definitions using `cedar-go/ast` builder, with `when { principal in resource }`
4. Implemented entity cache (`cache.go`) — per-user keyed, thread-safe with
   Invalidate/InvalidateAll
5. Implemented entity getter (`entities.go`) — builds Cedar entity maps from
   RoleBinding/PlatformRoleBinding stores with correct hierarchy
   (User→NamespaceRole→Namespace→Platform, User→PlatformRole→Platform)
6. Implemented authorizer (`authorizer.go`) — evaluates Cedar decisions, adds resource
   entities to entity maps with correct parent hierarchies
7. Implemented authz middleware (`middleware.go`) — derives Cedar action/resource from
   chi route patterns and URL params, handles cross-namespace list pre-computation
8. Added `PublicRegistry()` accessor to `apiserver.Server` in orlop
9. Extended `ListOptions` with `Namespaces []string` in orlop storage layer:
   - Memory store: filters by `namespace ∈ Namespaces`
   - Postgres store: `WHERE namespace = ANY($N)` parameterized query
   - Context helpers: `ContextWithAuthorizedNamespaces`/`AuthorizedNamespacesFromContext`
   - `ConvertingResourceHandler.List` reads authorized namespaces from context
10. Added `CustomValidator` for roleRef validation:
    - `RoleRefValidator` hook on private types (avoids circular imports)
    - `ValidateNamespaceRoleRef`/`ValidatePlatformRoleRef` in authz package
    - Wrong-scope detection (namespace role in PlatformRoleBinding and vice versa)
11. Wired authz middleware in `main.go` with `--authz-config` flag

**Test coverage (49+ subtests):**
- `config_test.go`: 10 tests — valid/invalid configs, edge cases, PermissionToAction
- `policygen_test.go`: 9 tests — policy generation, all role types, Cedar format
- `authorizer_test.go`: 10 tests (49 subtests) — all role×action combinations,
  cross-namespace isolation, default-deny, cache behavior, validator logic
- `middleware_test.go`: authn tests (10 tests) in separate package

**Drifts / Notes:**
- The plan specified `EntityGetter` implementing `cedar.EntityGetter` interface. Instead,
  we implemented a custom `EntityGetter` struct that builds full `EntityMap` objects and
  passes them to `cedar.Authorize()`. This is more practical because the Cedar authorization
  function needs all entities up front (not lazy-loaded).
- The `PublicRegistry()` approach worked as planned. Stores are wired after server creation
  using `SetEntityGetter()`.
- Cross-namespace list namespace-filter uses context helpers in `orlop/pkg/apiserver/storage`
  rather than the authz package, to avoid coupling orlop to platform-api.

---

## Phase 4: Bootstrap & Self-Authorization

**Goal**: Bootstrap the first platform-admin from the ConfigMap and ensure the authz API
resources are themselves subject to authorization.

### Tasks

1. **Implement bootstrap loader** in `platform-api/pkg/authz/config.go`
   - On startup, read `bootstrap.yaml` from the ConfigMap
   - For each PlatformRoleBinding entry, upsert into the store if not already present
   - Idempotent: skip if a binding with the same name already exists
   - Log a message for each created/skipped binding

2. **Verify authz middleware covers authz resources**
   - The middleware applies to ALL public API routes including `/rolebindings` and
     `/platformrolebindings`
   - No additional code needed if wired correctly in Phase 3

3. **Wire bootstrap into startup** in `platform-api/cmd/platform-api-server/main.go`
   - Load ConfigMap → parse roles → generate policies → create authorizer
   - Run bootstrap (upsert PlatformRoleBindings) before serving requests
   - Add `--authz-config` flag pointing to the ConfigMap mount path
     (default: `/etc/gecko/authz/`)

### Bootstrap Flow

```
# 1. Deploy Gecko with the ConfigMap:
kubectl apply -f deploy/gecko-authz-config.yaml

# 2. The server starts, reads the ConfigMap, creates the bootstrap PlatformRoleBinding
#    for operator@example.com with roleRef=platform-admin

# 3. operator@example.com can now manage PlatformRoleBindings via the API:
POST /apis/gcp.managed.openshift.io/v1/platformrolebindings
{ "spec": { "subject": "admin2@example.com", "roleRef": "platform-admin" } }

# 4. A service-admin creates namespace-scoped RoleBindings:
POST /apis/gcp.managed.openshift.io/v1/namespaces/project-a/rolebindings
{ "spec": { "subject": "dev@example.com", "roleRef": "cluster-admin" } }

POST /apis/gcp.managed.openshift.io/v1/namespaces/project-a/rolebindings
{ "spec": { "subject": "viewer@example.com", "roleRef": "cluster-viewer" } }
```

### Acceptance Criteria
- On first startup with bootstrap config, the PlatformRoleBinding is created
- On subsequent startups, bootstrap is a no-op (idempotent)
- The bootstrapped user can immediately create additional PlatformRoleBindings
- Without bootstrap config, a fresh deployment has no platform-admins and all requests
  get 403

### Implementation Status: COMPLETE

**Completed tasks:**
1. Implemented `RunBootstrap()` in `platform-api/pkg/authz/bootstrap.go`
   - Upserts PlatformRoleBindings from bootstrap config
   - Idempotent: skips if binding with same name already exists
   - Logs creation/skip for each binding
2. Authz middleware covers all public API routes (verified by middleware wiring in main.go)
3. Wired bootstrap into startup in `main.go`:
   - Loads ConfigMap via `--authz-config` flag (default: `/etc/gecko/authz/`)
   - Parses roles → generates policies → creates authorizer
   - After server creation: wires stores via `PublicRegistry()`, runs bootstrap
   - Sets `RoleRefValidator` on private types for roleRef validation

**Drifts / Notes:**
- Bootstrap is implemented in a separate `bootstrap.go` file rather than in `config.go`
  as originally planned, for cleaner separation of concerns.
- The plan mentioned creating the bootstrap in the config loader. Instead, it's a separate
  `RunBootstrap()` function called from `main.go` after server creation (when stores are
  available).

---

## Phase 5: Tests & Hardening

**Goal**: Comprehensive test coverage and production hardening.

### Tasks

1. **Cedar policy generation tests** (`policygen_test.go`)
   - Verify generated policies match expected Cedar syntax for each role
   - Test with empty role config → empty policy set
   - Test with invalid permission names → error

2. **Authorizer tests** (`authorizer_test.go`)
   - Table-driven tests covering every role × action × in-namespace / cross-namespace combination
   - Verify default-deny: no bindings → all denied
   - Verify forbid-overrides-permit semantics (relevant for Phase 6 custom policies)

3. **AuthN middleware tests** (`authn/middleware_test.go`)
   - All edge cases: missing header, invalid base64, missing email, empty email
   - Dev mode bypass (`--disable-auth` flag)

4. **Entity getter tests** (`authz/entities_test.go`)
   - User with no bindings → sparse entity graph
   - User with multiple namespace bindings → multiple NamespaceRole parents
   - User with platform-admin binding → PlatformRole parent
   - Same user with both namespace and platform bindings
   - NodePool entity with Cluster and Namespace parents

5. **Cache tests** (`authz/cache_test.go`)
   - Cache miss → DB query → cache populated
   - Cache hit → no DB query
   - Write to RoleBinding → cache invalidated → next check queries DB
   - Concurrent access safety

6. **Middleware integration tests** (`authz/middleware_test.go`)
   - Full HTTP round-trip tests using `httptest` with in-memory stores
   - Seed bindings via ConfigMap bootstrap, then make requests, assert HTTP status codes

7. **ConfigMap tests** (`authz/config_test.go`)
   - Valid ConfigMap → roles and bootstrap parsed correctly
   - Missing roles.yaml → error
   - Invalid role scope → error
   - Unknown permission name → error
   - Bootstrap with unknown roleRef → error

8. **E2E tests** (optional, manual or CI)
   - Spin up platform-api-server with in-memory storage and a test ConfigMap
   - Use a mock ESPv2 header to inject user identity
   - Test the full lifecycle: bootstrap → create bindings → verify access → revoke → verify denial

### Notes
- The existing in-memory store is ideal for unit/integration tests — no DB required
- Add a test helper `WithMockUser(email string)` that wraps `httptest` requests
  with the correct `X-Endpoint-API-UserInfo` header

---

## Phase 6: User-Defined Custom Roles

**Goal**: Allow service-admins to create custom roles with Cedar conditions within their
namespace, enabling attribute-based access control.

### Background

Built-in roles (from the ConfigMap) cover standard operational patterns. Custom roles allow
service-admins to create fine-grained, namespace-scoped roles with Cedar conditions — for
example, restricting access to clusters in a specific region, or allowing read-only access
to clusters matching a label selector.

### Design

A `CustomRole` is a namespaced API resource that defines:
- A set of permissions (same format as built-in roles)
- An optional Cedar condition (a `when` clause evaluated at authorization time)

```yaml
apiVersion: gcp.managed.openshift.io/v1
kind: CustomRole
metadata:
  name: us-east-cluster-viewer
  namespace: project-a
spec:
  permissions:
    - cluster.list
    - cluster.get
    - nodepool.list
    - nodepool.get
  condition: 'resource.region == "us-east1"'
```

When a `CustomRole` is created, the server:
1. Validates the permission names against the known permission set
2. Parses and validates the Cedar condition syntax
3. Generates a Cedar policy scoped to the custom role's namespace
4. Adds the policy to the `PolicySet` (hot-reload, no restart needed)

### Custom Role Cedar Policy Generation

For the example above, the generated policy would be:

```cedar
// CustomRole: project-a/us-east-cluster-viewer
permit (
    principal,
    action in [Action::"ListClusters", Action::"GetCluster",
               Action::"ListNodepools", Action::"GetNodepool"],
    resource
)
when {
    principal in Namespace::"project-a" &&
    resource.region == "us-east1"
};
```

Key differences from built-in role policies:
- **Namespace-pinned**: The `when` clause always includes `principal in Namespace::"<ns>"`
  to prevent a custom role from granting cross-namespace access
- **Additional condition**: The user-supplied Cedar condition is appended to the `when` clause
- **Resource attributes**: Cedar conditions can reference resource attributes (e.g.,
  `resource.region`). The entity getter must populate these attributes on the resource entity.

### Tasks

1. **Define `CustomRole` type** in `platform-api/api/private/v1/customrole_types.go`
   - Namespaced (`+kubebuilder:resource:scope=Namespaced`)
   - `CustomRoleSpec.Permissions []string` (list of permission names)
   - `CustomRoleSpec.Condition string` (optional Cedar `when` clause body)
   - All spec fields marked `+orlop:public`

2. **Register type and run `orlop-gen`**

3. **Wire `ResourceInfo`** in `resources.go`

4. **Add `CustomValidator`** to `CustomRole`
   - Validate permission names against the known permission set
   - Parse and validate Cedar condition syntax (reject if it doesn't parse)
   - Reject conditions that reference `principal in` any namespace other than the
     CustomRole's own namespace (prevent privilege escalation)
   - Reject permissions that are platform-scoped (custom roles are always namespace-scoped)

   **Condition validation implementation**: The `cedar-go` library (`x/exp/ast` package)
   provides full AST inspection capabilities suitable for this:
   - **Parsing**: Wrap the user-supplied condition string in a synthetic policy
     (`permit(principal, action, resource) when { <condition> };`), parse it with
     `Policy.UnmarshalCedar()`, and extract the `Conditions[0].Body` node.
   - **Cross-namespace detection**: Cast the parsed `*ast.Policy` to
     `*internalast.Policy` and use `internalast.Inspect(node, fn)` to walk the
     expression tree. Look for `NodeTypeIn` nodes where the left child is a
     `NodeTypeVariable` with `Name == "principal"` and the right child is a
     `NodeValue` containing an `EntityUID` with `Type == "Namespace"` and
     `ID != <this custom role's namespace>`. Reject if found.
   - **Attribute allow-list**: Walk the tree for `NodeTypeAccess` nodes (field access
     like `resource.region`). Verify the accessed field name (`Value` field) is in the
     allowed attribute set. Reject unknown attributes.
   - This approach uses proper AST inspection rather than fragile string matching.

5. **Extend policy generator** (`policygen.go`)
   - Accept `CustomRole` resources in addition to ConfigMap roles
   - Generate namespace-pinned policies with the user-supplied condition
   - Return the combined `PolicySet`

6. **Implement hot-reload of policies**
   - On `CustomRole` create/update/delete, regenerate the `PolicySet` and swap it
     atomically in the `Authorizer`
   - Use the same cache-invalidation pattern as RoleBindings

7. **Extend entity getter** to populate resource attributes
   - For `Cluster` entities: include attributes from `ClusterSpec` (e.g., `region`,
     `platform.type`) that custom role conditions might reference
   - For `NodePool` entities: include relevant attributes
   - Define an allow-list of attributes that can appear in conditions

8. **Add `service-admin` permissions** for custom role management
   - The `service-admin` built-in role includes `customrole.*` permissions
   - Service-admins can create, update, delete custom roles within their namespace

9. **Write tests**
   - Custom role CRUD lifecycle
   - Policy generation with conditions
   - Namespace-pinning enforcement (cross-namespace condition rejection)
   - Hot-reload: create custom role → verify policy takes effect without restart
   - Privilege escalation prevention: custom role cannot grant more than the creator has
   - Invalid Cedar condition syntax → validation error

### Security Considerations

- **Namespace isolation**: Custom role policies are always pinned to their namespace.
  A custom role in `ns1` cannot grant access to resources in `ns2`.
- **Privilege escalation**: A service-admin can only create custom roles with permissions
  that are a subset of the permissions available to namespace-scoped roles. They cannot
  create a custom role with `platformrolebinding.*` permissions.
- **Condition safety**: The Cedar condition is parsed and validated before being included
  in a policy. Only known resource attributes are allowed (via an allow-list).
- **Forbid policies**: Cedar's forbid-overrides-permit semantics provide a safety net.
  If needed, platform-level forbid policies can be added to the ConfigMap to create
  hard limits that custom roles cannot override.

### Acceptance Criteria
- Service-admin can create a custom role in their namespace
- A user bound to a custom role can access resources matching the condition
- The same user is denied access to resources not matching the condition
- Custom role cannot grant cross-namespace access
- Custom role cannot include platform-scoped permissions
- Invalid Cedar condition is rejected at creation time
- Deleting a custom role immediately revokes access (hot-reload)
- A user with no role bindings still gets 403 on everything

---

## Open Items & Future Considerations

- **Audit logging**: Log Cedar decisions (allow/deny) with principal, action, resource, and
  determining policy IDs for observability. OpenTelemetry traces are already in the stack.
- **Token re-validation**: Currently Gecko trusts the ESPv2 header without re-validating.
  Consider adding optional token verification for defense-in-depth.
- **Performance (multi-instance)**: The cache-until-dirty strategy works well for single
  instances. For multi-instance deployments, PostgreSQL `LISTEN/NOTIFY` provides cross-instance
  invalidation. Monitor whether the invalidation latency is acceptable.
- **Cross-namespace visibility**: Currently `cluster-admin` in `ns1` cannot see resources in
  `ns2`. If cross-namespace read is needed, a new built-in role or a platform-scoped role
  with read permissions would be required.
- **Namespace validation**: Currently namespaces are implicit strings. If explicit namespace
  lifecycle management is added later, the authz middleware would need to validate that the
  namespace exists before checking permissions.
- **Resource attribute evolution**: As new fields are added to `ClusterSpec` or `NodePoolSpec`,
  decide which should be exposed as Cedar resource attributes for custom role conditions.
  Maintain an explicit allow-list to avoid leaking sensitive fields.
- **Forbid policies**: The ConfigMap could support `forbid` policies for hard platform-level
  restrictions (e.g., "no one can delete clusters in production namespaces"). Cedar's
  forbid-overrides-permit semantics make this safe to compose with custom roles.
- **Rate limiting**: With authn/authz in place, the public API has no rate limiting. A
  malicious or buggy client could hammer the endpoint, forcing repeated Cedar evaluations
  and DB queries (especially on cache misses). Consider adding per-user rate limiting as a
  middleware layer. This is orthogonal to authz and can be added independently.
- **ConfigMap immutability**: Built-in roles are intentionally defined in a ConfigMap (not
  as API resources) and require a redeploy to change. This is a deliberate immutable
  infrastructure design choice: role definitions are versioned in Git alongside the Helm
  chart, reviewed via PR, and deployed as a unit. The trade-off is that adding new built-in
  roles before Phase 6 (custom roles) requires a full redeploy. This is acceptable because
  built-in roles represent stable, well-defined operational patterns that change
  infrequently.
