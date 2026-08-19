# Cedar Authorization — Developer Guide

This document covers the internals of gecko's Cedar-based authorization system. It is intended for developers working on the platform-api-server or any component that interacts with the authorization layer.

## Architecture Overview

```
┌──────────────┐       ┌──────────────────┐       ┌────────────────┐
│  ArgoCD/     │──────>│  Aggregated API  │──────>│   Database     │
│  Helm        │ apply │  Server (:8443)  │ write │  (Roles,       │
│  (Platform   │       │                  │       │   Bindings)    │
│   Roles)     │       │                  │       │                │
└──────────────┘       └──────────────────┘       └───────┬────────┘
                                                          │ read
                                                          ▼
┌──────────────┐       ┌──────────────────┐       ┌────────────────┐
│  Client      │──────>│  platform-api    │──────>│  Cedar Engine  │
│  (curl/UI)   │ HTTP  │  server (:8081)  │       │  (PolicySet +  │
│              │       │  authn -> authz  │       │   Entities)    │
└──────────────┘       └──────────────────┘       └────────────────┘
```

1. **PlatformRoles** are deployed via Helm/ArgoCD and applied through the aggregated API server.
2. The **platform-api-server** (public API on port 8081) loads roles and bindings from the database, constructs a Cedar PolicySet and entity graph, and evaluates every incoming request.
3. The **private API** (Kubernetes aggregated API server on port 8443) does not run Cedar middleware — it is internal only.

## Module Structure

| Path | Purpose |
|---|---|
| `platform-api/pkg/authz/` | Cedar engine, PolicySet builder, entity graph, authorization middleware |
| `platform-api/pkg/authn/` | Authentication middleware (JWT extraction from ESPv2 header, dev mode) |
| `orlop/` | Shared types, storage interfaces, and generated code for Role/RoleBinding |
| `helm/charts/platform-api-server/templates/platformroles/` | System PlatformRole manifest templates |

### Key files in `platform-api/pkg/authz/`

- **authorizer.go** — Core Cedar engine: builds the `PolicySet` from roles, constructs the entity graph from bindings, and exposes `Authorize()` and `AuthorizeWithContext()` methods.
- **middleware.go** — chi middleware that extracts action/resource from the URL, calls the engine, and returns 403 on deny.
- **policygen.go** — Converts PlatformRole and Role objects (with their permissions) into Cedar policy strings.
- **entities.go** — Builds the Cedar entity graph (User, NamespaceRole, Namespace entities and relationships). Also exposes `AuthorizedNamespaces()` for cross-namespace list filtering.
- **reload.go** — Watch-based hot-reload: listens for changes to roles/bindings and reloads the PolicySet. Invalidates the full entity cache on role/platform-role changes; invalidates only the affected user's cache on RoleBinding changes.
- **permissions.go** — Canonical lists of permission strings, Cedar action names, and their mappings.

### Key files in `platform-api/pkg/authn/`

- **middleware.go** — Extracts user identity from the `X-Endpoint-API-UserInfo` header (base64-encoded JWT claims) or `X-Dev-User` in dev mode.
- **context.go** — `WithUser`/`UserFromContext` helpers that place the user email into `context.Context`.

## System PlatformRoles

PlatformRoles are deployed via the `platform-api-server` Helm chart (located in `helm/charts/platform-api-server/templates/platformroles/`). They are cluster-scoped resources marked with `spec.system: true` and are never modified by end users through the public API.

To enable or disable deployment of system PlatformRoles, set `platformRoles.enabled` in the Helm values:

```yaml
platformRoles:
  enabled: true  # Set to false if PlatformRoles are managed externally
```

### Built-in System PlatformRoles

| PlatformRole | Type | Permissions |
|---|---|---|
| `cluster-viewer` | PlatformRole (cluster-scoped) | `cluster.list`, `cluster.get`, `nodepool.list`, `nodepool.get` |
| `cluster-admin` | PlatformRole (cluster-scoped) | Full CRUD on clusters and nodepools |
| `service-admin` | PlatformRole (cluster-scoped) | Full CRUD on roles and rolebindings |

### User-defined Roles

Users with the `service-admin` PlatformRole can create namespace-scoped `Role` objects via the public API. These Roles may not include infrastructure write permissions (`cluster.create/update/delete`, `nodepool.create/update/delete`) — those are reserved for PlatformRoles.

## Cedar Engine

### PolicySet generation

When the engine loads (or reloads), it:

1. Fetches all **PlatformRoles**, namespace-scoped **Roles**, and **RoleBindings** from the database.
2. For each **PlatformRole**, generates one policy **per RoleBinding** that references it. The `NamespaceRole` entity is keyed by `ns/roleName/bindingName` — unique per binding — so policies from different bindings cannot interfere with each other:
   ```cedar
   permit (principal, action in [Action::"ListClusters", Action::"GetCluster"], resource)
   when { principal in NamespaceRole::"my-ns/cluster-viewer/alice-viewer" && resource in Namespace::"my-ns" };
   ```
3. For each namespace-scoped **Role**, generates one policy **per RoleBinding** that references it. The `NamespaceRole` key is also `ns/roleName/bindingName`. The Cedar condition comes from the **RoleBinding** (not the Role), making each binding's policy fully independent:
   ```cedar
   permit (principal, action in [Action::"ListClusters", Action::"GetCluster"], resource)
   when {
     principal in NamespaceRole::"org-1/my-role/user2-binding" &&
     resource in Namespace::"org-1" &&
     (!(context has resourceName) || context.spec.region == "us-east1")
   };
   ```
   The `!(context has resourceName)` guard ensures that list-level authorization (no `resourceName` in context) succeeds, while per-item filtering applies the condition to each result individually.
4. All policies are combined into a single `PolicySet`.

**Note**: The Cedar condition is stored on the **RoleBinding** (`spec.condition`), not on the Role. This allows the same Role to be bound to different users with different conditions, and ensures that an unconditional binding for one user cannot bypass the condition of another user's binding to the same role.

### Cedar context

Every authorization request carries a Cedar `context` record populated by the middleware:

| Attribute | Type | Description |
|---|---|---|
| `context.resourceName` | string | Name from URL path (absent for list operations) |
| `context.resourcePlural` | string | Resource kind plural (`clusters`, `nodepools`, etc.) |
| `context.method` | string | HTTP method (`GET`, `POST`, `PUT`, `DELETE`) |
| `context.spec` | record | Full nested spec from request body (POST/PUT) or object fields (per-item filter) |

For list operations, `resourceName` is **absent** from the context. Per-item filtering re-evaluates each result with the item's attributes in the context.

### Entity graph

The entity graph maps the relationships between users, namespaces, and role hierarchies:

- **User::"email"** — created from binding subjects.
- **NamespaceRole::"ns/roleName/bindingName"** — one entity per RoleBinding, linking a user to a specific binding in a specific namespace. Keyed per binding (not per role) so that an unconditional binding for one user cannot satisfy another user's conditioned policy for the same role.
- **Namespace::"ns"** — leaf entity representing the target namespace.
- A RoleBinding creates: `User → NamespaceRole::"ns/roleName/bindingName" → Namespace::"ns"`.

### Authorization flow

1. The authz middleware extracts the **action** (e.g., `GetCluster`) and **namespace** from the HTTP request.
2. It builds a Cedar **context** record with resource attributes from the request.
3. It calls `authorizer.AuthorizeWithContext(ctx, user, action, namespace, cedarCtx)`.
4. The Cedar engine evaluates the PolicySet against the entity graph and context.
5. If the result is `Allow`, the request proceeds. Otherwise, 403.
6. For **list operations**, an `ItemFilter` is injected into the handler context. Each list item is evaluated independently with the item's attributes in the Cedar context, so items failing the condition are excluded from the response.

## Authentication Middleware

### Production mode

The public API sits behind ESPv2, which validates JWTs and forwards claims in the `X-Endpoint-API-UserInfo` header.

1. The middleware reads the `X-Endpoint-API-UserInfo` header.
2. It base64-decodes the value (raw URL encoding, no padding) to get the JSON JWT claims.
3. It extracts the `email` field as the user identity.
4. If the header is missing, malformed, or lacks an `email` claim, the middleware returns **401 Unauthorized**.
5. On success, it stores the user email in the request context via `authn.WithUser`.

### Dev mode

When the platform-api-server is started with the `--disable-auth` flag:

1. The middleware reads the `X-Dev-User` header instead.
2. The header value is used directly as the user email — no JWT validation.
3. If the `X-Dev-User` header is missing, the middleware returns **401 Unauthorized**.

Note: when `--disable-auth` is set the **authz** middleware also skips all authorization checks entirely (not just authn). This is for local development only.

## Authorization Middleware

The authz middleware is a chi middleware that runs after authn.

### URL path parsing

The middleware expects Kubernetes API-style paths:

```
/apis/{group}/{version}/namespaces/{namespace}/{plural}           -> namespaced list/create
/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}    -> get/update/delete
/apis/{group}/{version}/{plural}                                  -> cross-namespace list
```

- **Resource type**: Extracted from the `{plural}` path segment.
- **Namespace**: Extracted from the `{namespace}` segment for namespaced resources.
- **Action**: Derived from the HTTP method and whether a name is present:

| Method | Name present? | Action |
|---|---|---|
| GET | Yes | `Get{Resource}` |
| GET | No | `List{Resources}` |
| POST | No | `Create{Resource}` |
| PUT | Yes | `Update{Resource}` |
| DELETE | Yes | `Delete{Resource}` |

### Pre-filter vs namespace-filter

- **Namespaced requests** (e.g., `GET /apis/.../namespaces/my-ns/clusters`): the middleware authorizes the request for the specific namespace. For list operations, an `ItemFilter` is also injected for per-item condition evaluation.
- **Cross-namespace list** (e.g., `GET /apis/.../clusters` without `namespaces/` segment): the middleware calls `AuthorizedNamespaces` to find all namespaces the user has the action permission in, injects that list into the handler context, and also attaches an `ItemFilter`. If the user has **zero** authorized namespaces, the middleware returns **403 Forbidden**.

### Cross-namespace list handling

When a user calls a cross-namespace list (no namespace in path):

1. The middleware determines all namespaces the user has the list permission in.
2. If zero namespaces are authorized, it returns **403 Forbidden**.
3. Otherwise it attaches the namespace list to the request context so the storage layer queries only those namespaces.
4. An `ItemFilter` is also attached for per-item Cedar condition evaluation.

## Hot-Reload Mechanism

The Cedar engine supports hot-reload so that role and binding changes take effect without restarting the platform-api-server.

1. **Watch-based**: `StartWatching(ctx)` sets up database watches on PlatformRole, Role, and RoleBinding tables.
2. **On PlatformRole or Role change**: The engine rebuilds the PolicySet and **invalidates the entire entity cache** (all users).
3. **On RoleBinding change**: The engine rebuilds the PolicySet and **invalidates only the affected user's** cached entity graph (extracted from `spec.subject`).
4. **Convergence**: After a change, the new policy takes effect on the next request (no delay beyond the watch propagation).

## Validator Design

Validators enforce business rules on create/update operations for roles and bindings. They run in the admission path of the public API.

### Circular import avoidance

Validators need access to the database (e.g., to check if a roleRef exists) but live in a package that cannot import the storage layer directly. This is solved with a `ValidatorDeps` struct registered globally at startup:

```go
type ValidatorDeps struct {
    RoleExists         func(ctx context.Context, namespace, name string) bool
    PlatformRoleExists func(ctx context.Context, name string) bool
}
```

The storage implementation satisfies these function fields and is injected via `SetValidatorDeps(deps)` at startup.

### Validations

| Validator | Applies to | Rule |
|---|---|---|
| Permission name validation | Role | Every permission in the role must be in the known permission set |
| Infra write restriction | Role (user-defined) | User-defined roles cannot include infrastructure write permissions |
| Subject required | RoleBinding | `spec.subject` must not be empty |
| RoleRef kind validation | RoleBinding | `roleRef.kind` must be `"PlatformRole"` or `"Role"` |
| RoleRef apiGroup validation | RoleBinding | `roleRef.apiGroup` must be the gecko API group |
| RoleRef existence check | RoleBinding | The referenced role must exist in the database |
| Self-grant prevention | RoleBinding | The caller cannot create a binding where the subject is their own email |
| Cedar condition validation | RoleBinding | `spec.condition` must be valid Cedar syntax and must not reference `Namespace::` entities |

**Note**: The `spec.condition` (Cedar condition) lives on the **RoleBinding**, not on the Role. Role validation does not handle conditions. Cedar condition validation runs on **create and update** (not delete). An invalid condition (bad syntax or `Namespace::` reference) must be caught at admission time; if an invalid condition reaches the policy engine, all subsequent policy reloads will fail with a parse error, leaving the authorizer running on a stale policy set.

## Storage Wiring in main.go

The platform-api-server uses a **shared memoized factory pattern** for database connections:

1. A single factory function creates the database connection pool.
2. The factory is memoized — calling it multiple times returns the same pool.
3. Both the Cedar engine and the API handlers receive their storage interfaces from this shared pool.
4. This ensures a single connection pool is used across the entire server, avoiding connection exhaustion.

```go
// Simplified example
storageFactory := sync.OnceValues(func() (Storage, error) {
    return NewStorage(dbConfig)
})

authzEngine := authz.NewAuthorizer(ctx, authz.AuthzStores{...})
handlers := api.NewHandlers(storageFactory)
```

## How to Add a New Permission

1. **Define the permission string** in `platform-api/pkg/authz/permissions.go` (e.g., `widget.create`). Add it to `PermissionToAction`, `ResourcePluralToActions`, and `ResourceSingularGetAction` as appropriate.
2. **Mirror it** in `platform-api/api/private/v1/validation.go`'s `validPermissions` map (kept in sync manually to avoid circular imports).
3. **Decide if it should be allowed in user-defined roles**: If no (infrastructure write), add it to `InfraWritePermissions` in `permissions.go` and `infraWritePermissions` in `validation.go`.
4. **Add the permission to the appropriate system role(s)** in `helm/charts/platform-api-server/templates/platformroles/`.
5. **Write tests**: Add unit tests for the permission in both the engine and middleware test suites.
6. **Deploy**: Update the Helm chart and deploy via ArgoCD.

## How to Add a New System Role

1. **Create a new PlatformRole manifest** in `helm/charts/platform-api-server/templates/platformroles/platformrole-<name>.yaml` with the role's name and permissions list.
2. **Deploy the updated Helm chart** via ArgoCD or `helm upgrade`.
3. **Write tests**: Add test cases verifying the new role's permissions are enforced correctly.

## Running Tests

Each module has its own test suite. Run tests from the module directory:

```bash
# Authorization engine and middleware tests
cd platform-api
make test

# Shared types and storage tests
cd orlop
make test
```

For integration tests that require a database, ensure a local database is running or use the test fixtures provided in each module's `testdata/` directory.
