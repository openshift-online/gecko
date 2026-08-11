# Cedar Authorization — Testing Guide

This document describes how to test the Cedar-based authorization system step by step.

## Prerequisites

```bash
cd platform-api
go build -o platform-api-server ./cmd/platform-api-server
```

## 1. Automated Tests

Run the full test suite. This covers all Cedar policy logic, entity graph construction,
cache behavior, config parsing, roleRef validation, and resource CRUD.

```bash
cd platform-api
go test ./... -v -count=1
```

**Expected**: 59 tests, 144+ subtests, all pass.

---

## 2. Manual Test: Auth Disabled (Smoke Test)

This verifies basic CRUD works with no authentication or authorization.

### Start the server

```bash
./platform-api-server --disable-auth
```

The server starts on `127.0.0.1:18081` (public API) with in-memory storage.
No database, no TLS certificates, no Kubernetes cluster needed.

### Run the tests

```bash
# Health check
curl http://localhost:8081/healthz
# Expected: "ok" [200]

# List clusters (empty)
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/default/clusters
# Expected: {"items":[], ...} [200]

# Create a RoleBinding
curl -X POST http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings \
  -H "Content-Type: application/json" \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"RoleBinding",
       "metadata":{"name":"test-rb","namespace":"ns1"},
       "spec":{"subject":"dev@example.com","roleRef":"cluster-admin"}}'
# Expected: 201 Created

# Get it back
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings/test-rb
# Expected: 200 with the binding

# Create a PlatformRoleBinding (cluster-scoped, no namespace)
curl -X POST http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings \
  -H "Content-Type: application/json" \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"PlatformRoleBinding",
       "metadata":{"name":"test-prb"},
       "spec":{"subject":"admin@example.com","roleRef":"platform-admin"}}'
# Expected: 201 Created

# List PlatformRoleBindings
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings
# Expected: 200 with the binding

# Cross-namespace list RoleBindings
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/rolebindings
# Expected: 200, shows test-rb

# Delete
curl -X DELETE http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings/test-rb
# Expected: 200

# Verify deletion
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings/test-rb
# Expected: 404
```

Stop the server with Ctrl+C.

---

## 3. Manual Test: RoleRef Validation

This verifies that RoleBinding and PlatformRoleBinding reject invalid roleRef values.
Requires the authz config to be loaded (so the server knows which roles exist).

### Create the authz config

```bash
mkdir -p /tmp/gecko-authz

cat > /tmp/gecko-authz/roles.yaml << 'EOF'
roles:
  - name: cluster-viewer
    scope: namespace
    permissions: [cluster.list, cluster.get, nodepool.list, nodepool.get]
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
EOF

cat > /tmp/gecko-authz/bootstrap.yaml << 'EOF'
platformRoleBindings:
  - name: bootstrap-admin
    subject: admin@example.com
    roleRef: platform-admin
EOF
```

### Start the server

```bash
./platform-api-server --disable-auth --authz-config=/tmp/gecko-authz
```

Log should show: `Loaded 4 roles from authz config` and
`Bootstrap: created PlatformRoleBinding "bootstrap-admin"`.

### Run the tests

```bash
# RoleBinding with a platform-scoped role -> 400
curl -X POST http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings \
  -H "Content-Type: application/json" \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"RoleBinding",
       "metadata":{"name":"bad1","namespace":"ns1"},
       "spec":{"subject":"a@b.com","roleRef":"platform-admin"}}'
# Expected: 400 "platform-scoped role and cannot be used in a namespace-scoped RoleBinding"

# RoleBinding with an unknown role -> 400
curl -X POST http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings \
  -H "Content-Type: application/json" \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"RoleBinding",
       "metadata":{"name":"bad2","namespace":"ns1"},
       "spec":{"subject":"a@b.com","roleRef":"does-not-exist"}}'
# Expected: 400 "not a known role"

# PlatformRoleBinding with a namespace-scoped role -> 400
curl -X POST http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings \
  -H "Content-Type: application/json" \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"PlatformRoleBinding",
       "metadata":{"name":"bad3"},
       "spec":{"subject":"a@b.com","roleRef":"cluster-admin"}}'
# Expected: 400 "namespace-scoped role and cannot be used in a PlatformRoleBinding"

# Valid RoleBinding -> 201
curl -X POST http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings \
  -H "Content-Type: application/json" \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"RoleBinding",
       "metadata":{"name":"ok","namespace":"ns1"},
       "spec":{"subject":"a@b.com","roleRef":"cluster-admin"}}'
# Expected: 201 Created

# Bootstrap binding was auto-created
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings/bootstrap-admin
# Expected: 200, shows subject=admin@example.com, roleRef=platform-admin
```

Stop the server with Ctrl+C.

---

## 4. Manual Test: Auth Enabled (RBAC Enforcement)

This verifies that Cedar authorization correctly allows and denies requests
based on the user's role bindings.

### Helper: Create the auth header

The public API expects an `X-Endpoint-API-UserInfo` header containing
base64-encoded JSON with an `email` field (this is what ESPv2 injects
in production).

```bash
# Shell function to create the header value for a given email
ui() { echo -n "{\"email\":\"$1\"}" | base64; }

# Example:
#   ui admin@example.com
#   -> eyJlbWFpbCI6ImFkbWluQGV4YW1wbGUuY29tIn0=
```

### Start the server (auth enabled)

```bash
./platform-api-server \
  --address=127.0.0.1 \
  --authz-config=/tmp/gecko-authz
```

Uses the same config from step 3. The bootstrap creates a `platform-admin`
binding for `admin@example.com`.

### Run the tests

```bash
ui() { echo -n "{\"email\":\"$1\"}" | base64; }

# Health check (no auth required)
curl http://localhost:8081/healthz
# Expected: "ok" [200]

# No auth header -> 401
curl http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings
# Expected: 401 Unauthenticated

# platform-admin lists PlatformRoleBindings -> 200
curl -H "X-Endpoint-API-UserInfo: $(ui admin@example.com)" \
  http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings
# Expected: 200, shows bootstrap-admin

# platform-admin lists clusters (no namespace binding) -> 403
curl -H "X-Endpoint-API-UserInfo: $(ui admin@example.com)" \
  http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/clusters
# Expected: 403 Forbidden

# nobody lists PlatformRoleBindings -> 403
curl -H "X-Endpoint-API-UserInfo: $(ui nobody@example.com)" \
  http://localhost:8081/apis/gcp.managed.openshift.io/v1/platformrolebindings
# Expected: 403 Forbidden

# platform-admin creates a RoleBinding (wrong scope) -> 403
curl -X POST -H "X-Endpoint-API-UserInfo: $(ui admin@example.com)" \
  -H "Content-Type: application/json" \
  http://localhost:8081/apis/gcp.managed.openshift.io/v1/namespaces/ns1/rolebindings \
  -d '{"apiVersion":"gcp.managed.openshift.io/v1","kind":"RoleBinding",
       "metadata":{"name":"x","namespace":"ns1"},
       "spec":{"subject":"x@x.com","roleRef":"service-admin"}}'
# Expected: 403 Forbidden (platform-admin has no namespace-scoped permissions)

# Cross-namespace list with no bindings -> empty list (not 403)
curl -H "X-Endpoint-API-UserInfo: $(ui nobody@example.com)" \
  http://localhost:8081/apis/gcp.managed.openshift.io/v1/clusters
# Expected: 200 with empty items list
```

Stop the server with Ctrl+C.

---

## 5. Validation Checklist

| # | Test | Expected | Auth Mode |
|---|------|----------|-----------|
| 1 | `GET /healthz` | 200 `ok` | any |
| 2 | API request without auth header | 401 | enabled |
| 3 | platform-admin lists PRBs | 200 | enabled |
| 4 | platform-admin lists clusters | 403 | enabled |
| 5 | nobody lists PRBs | 403 | enabled |
| 6 | platform-admin creates RoleBinding | 403 | enabled |
| 7 | Cross-ns list, no bindings | 200, empty | enabled |
| 8 | RoleBinding with platform-scoped role | 400 | disabled+config |
| 9 | RoleBinding with unknown role | 400 | disabled+config |
| 10 | PlatformRoleBinding with ns-scoped role | 400 | disabled+config |
| 11 | Valid RoleBinding creation | 201 | disabled+config |
| 12 | Bootstrap PRB auto-created | 200 | disabled+config |
| 13 | Full CRUD (create, get, list, delete) | 201/200/200/200 | disabled |
| 14 | Cluster-scoped CRUD (PlatformRoleBinding) | 201/200/200/200 | disabled |

---

## Known Limitations

### Cross-namespace list filtering with seeded data

**What it is**: When auth is enabled, cross-namespace list requests (e.g.,
`GET /apis/.../clusters` without a namespace) should return only clusters from
namespaces where the user has the required permission. The Cedar authorization
logic for this is fully implemented and tested in unit tests.

**What doesn't work yet in manual testing**: To test this end-to-end with a live
server, you need:
1. RoleBindings granting a user access to some namespaces but not others
2. Clusters in multiple namespaces
3. A cross-namespace list request to verify filtering

The problem is seeding this data. With in-memory storage, data doesn't survive
server restarts. With auth enabled, creating RoleBindings requires a user with
`service-admin` role, which itself requires a RoleBinding — a chicken-and-egg
problem.

The bootstrap only creates PlatformRoleBindings (platform-scoped). There is no
bootstrap mechanism for namespace-scoped RoleBindings yet.

**Workarounds** (pick one):

1. **Use PostgreSQL** — data persists across restarts, so you can seed with
   `--disable-auth` and restart with auth enabled:
   ```bash
   docker run -d --name gecko-db \
     -e POSTGRES_USER=orlop -e POSTGRES_PASSWORD=orlop -e POSTGRES_DB=orlop \
     -p 5432:5432 postgres:16

   # Seed data (auth disabled)
   DB_HOST=localhost DB_USER=orlop DB_PASSWORD=orlop \
     ./platform-api-server --disable-auth --authz-config=/tmp/gecko-authz
   # Create rolebindings and clusters via curl, then Ctrl+C

   # Test RBAC (auth enabled, same database)
   DB_HOST=localhost DB_USER=orlop DB_PASSWORD=orlop \
     ./platform-api-server --address=127.0.0.1 --authz-config=/tmp/gecko-authz
   # Test with X-Endpoint-API-UserInfo header
   ```

2. **Add namespace-scoped bootstrap** — extend `bootstrap.yaml` to support
   `roleBindings` entries (requires a code change to `RunBootstrap()`).

3. **Use the private API** — the private API (port 8080, HTTPS) uses Kubernetes
   delegated auth. With `--disable-auth`, it allows anonymous access. However,
   there is a known issue where objects created via the private API's aggregated
   k8s apiserver don't appear in list queries from the same in-memory store.
   This appears to be a pre-existing framework issue in orlop, not related to
   the authz changes.

### What the unit tests already cover

The automated tests (`authorizer_test.go`) comprehensively test cross-namespace
behavior using in-memory stores seeded directly:

- User with `cluster-viewer` in `ns1` can list/get clusters in `ns1` (Allow)
- Same user cannot list/get clusters in `ns2` (Deny)
- User with bindings in `ns1` and `ns2` — `AuthorizedNamespaces()` returns
  both namespaces
- User with no bindings — `AuthorizedNamespaces()` returns empty list
- The `ListOptions.Namespaces` filter is tested in the orlop memory store
