# platform-api on kind

Local authz testing environment using [kind](https://kind.sigs.k8s.io/).

The private API (port 8443) uses kind's kube-apiserver for delegated
authn/authz, so `kubectl` works normally for seeding platform roles and
creating namespace-scoped bindings.
The public API (port 8081) runs the Cedar authorization middleware.

## Prerequisites

- `kind`
- `kubectl`
- `docker` or `podman`
- `curl`, `jq`

## Setup

```sh
./deploy/kind/setup.sh
```

This creates a kind cluster named `gecko-dev`, installs cert-manager, builds
and loads the container image (platform-api-server), deploys all resources,
waits for the rollout, and creates system PlatformRoles via kubectl.
The cluster is left running when the script exits.

## Port-forward the public API

In a separate terminal (keep it running while you test):

```sh
kubectl -n gecko-system port-forward svc/platform-api-server-public 8081:8081
```

## Role model

| Type | Scope | Created by | Purpose |
|---|---|---|---|
| **PlatformRole** | Cluster | Helm/kubectl (aggregated API) | System permission definitions |
| **Role** | Namespace | service-admin (public API) | Custom permissions within a namespace |
| **RoleBinding** | Namespace | service-admin (public API) | Grants a PlatformRole or Role to a user |

System PlatformRoles created via kubectl: `cluster-viewer`, `cluster-admin`, `service-admin`.

## Seeding bindings via kubectl (private API)

Use `kubectl` to create namespace-level bindings via the private API. Namespaces
must exist first.

```sh
# Create test namespaces
kubectl create namespace org-1
kubectl create namespace org-2

# Bind alice to cluster-viewer (PlatformRole) in org-1
kubectl apply -f - <<'EOF'
apiVersion: gcp.managed.openshift.io/v1
kind: RoleBinding
metadata:
  name: alice-viewer
  namespace: org-1
spec:
  subject: alice@example.com
  roleRef:
    kind: PlatformRole
    name: cluster-viewer
    apiGroup: gcp.managed.openshift.io
EOF

# Bind bob to cluster-admin (PlatformRole) in org-2
kubectl apply -f - <<'EOF'
apiVersion: gcp.managed.openshift.io/v1
kind: RoleBinding
metadata:
  name: bob-admin
  namespace: org-2
spec:
  subject: bob@example.com
  roleRef:
    kind: PlatformRole
    name: cluster-admin
    apiGroup: gcp.managed.openshift.io
EOF
```

Verify:

```sh
kubectl get platformroles.gcp.managed.openshift.io
kubectl get rolebindings.gcp.managed.openshift.io -A
```

## Creating a custom namespace-scoped Role (public API)

service-admins can create custom Roles within their namespace via the public API.
Custom Roles cannot include infrastructure write permissions (those are PlatformRole-only).

```sh
userinfo() { printf '{"email":"%s","sub":"%s"}' "$1" "$1" | base64 | tr -d '=' | tr '+/' '-_'; }
BASE="http://localhost:8081/apis/gcp.managed.openshift.io/v1"

# First bind a service-admin (via kubectl)
kubectl apply -f - <<'EOF'
apiVersion: gcp.managed.openshift.io/v1
kind: RoleBinding
metadata:
  name: alice-service-admin
  namespace: org-1
spec:
  subject: alice@example.com
  roleRef:
    kind: PlatformRole
    name: service-admin
    apiGroup: gcp.managed.openshift.io
EOF

# alice creates a custom read-only role in org-1
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  -H "Content-Type: application/json" \
  -X POST $BASE/namespaces/org-1/roles \
  -d '{"metadata":{"name":"cluster-ro","namespace":"org-1"},"spec":{"permissions":["cluster.list","cluster.get"]}}'

# alice binds user1@example.com to cluster-ro in org-1
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  -H "Content-Type: application/json" \
  -X POST $BASE/namespaces/org-1/rolebindings \
  -d '{
    "metadata":{"name":"user1-ro","namespace":"org-1"},
    "spec":{
      "subject":"user1@example.com",
      "roleRef":{"kind":"Role","name":"cluster-ro","apiGroup":"gcp.managed.openshift.io"}
    }
  }'
```

## Condition-based bindings

A RoleBinding can include an optional Cedar condition that restricts access to
specific resources. The condition is evaluated against the request context:

| Context attribute | Description |
|---|---|
| `context.resourceName` | Name of the resource being accessed |
| `context.resourcePlural` | Resource type (`clusters`, `nodepools`, etc.) |
| `context.method` | HTTP method |
| `context.spec` | Full nested spec of the resource |

```sh
# Bind user1 to cluster-ro, but only for cluster named "my-cluster"
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  -H "Content-Type: application/json" \
  -X POST $BASE/namespaces/org-1/rolebindings \
  -d '{
    "metadata":{"name":"user1-ro-mycluster","namespace":"org-1"},
    "spec":{
      "subject":"user1@example.com",
      "roleRef":{"kind":"Role","name":"cluster-ro","apiGroup":"gcp.managed.openshift.io"},
      "condition":"context.resourceName == \"my-cluster\""
    }
  }'

# user1 can get my-cluster -> 200
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo user1@example.com)" \
  $BASE/namespaces/org-1/clusters/my-cluster

# user1 cannot get other-cluster -> 403
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo user1@example.com)" \
  $BASE/namespaces/org-1/clusters/other-cluster

# user1 lists clusters -> only my-cluster is returned (condition filters)
curl -s -H "X-Endpoint-API-UserInfo: $(userinfo user1@example.com)" \
  $BASE/namespaces/org-1/clusters | jq '.items[].metadata.name'
```

## Testing Cedar authorization

The public API requires an `X-Endpoint-API-UserInfo` header containing
base64url-encoded JSON with an `email` field (no padding, same as ESPv2).

Shell helper:

```sh
userinfo() {
  printf '{"email":"%s","sub":"%s"}' "$1" "$1" \
    | base64 | tr -d '=' | tr '+/' '-_'
}
BASE="http://localhost:8081/apis/gcp.managed.openshift.io/v1"
```

### Authentication checks

```sh
# No header -> 401
curl -s -w "\n%{http_code}" $BASE/namespaces/org-1/clusters

# Malformed header -> 401
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: %%%not-valid%%%" \
  $BASE/namespaces/org-1/clusters

# Valid base64 but no email claim -> 401
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(printf '{"sub":"noemail"}' | base64 | tr -d '=' | tr '+/' '-_')" \
  $BASE/namespaces/org-1/clusters
```

### Authorization checks

```sh
# alice lists clusters in org-1 -> 200 (viewer binding)
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  $BASE/namespaces/org-1/clusters

# alice lists clusters in org-2 -> 403 (no binding)
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  $BASE/namespaces/org-2/clusters

# unknown user -> 403
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo stranger@example.com)" \
  $BASE/namespaces/org-1/clusters

# alice tries to create a cluster (viewer can't) -> 403
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  -H "Content-Type: application/json" \
  -X POST $BASE/namespaces/org-1/clusters \
  -d '{"metadata":{"name":"test","namespace":"org-1"},"spec":{"platform":{"type":"GCP"},"release":{"version":"4.18.0","channelGroup":"stable"},"networking":{"networkType":"OVNKubernetes"}}}'

# bob creates a cluster in org-2 (cluster-admin) -> 201
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo bob@example.com)" \
  -H "Content-Type: application/json" \
  -X POST $BASE/namespaces/org-2/clusters \
  -d '{"metadata":{"name":"test","namespace":"org-2"},"spec":{"platform":{"type":"GCP"},"release":{"version":"4.18.0","channelGroup":"stable"},"networking":{"networkType":"OVNKubernetes"}}}'
```

### Cross-namespace list

```sh
# alice sees clusters only from org-1
curl -s -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  $BASE/clusters | jq .

# bob sees clusters only from org-2
curl -s -H "X-Endpoint-API-UserInfo: $(userinfo bob@example.com)" \
  $BASE/clusters | jq .
```

### Hot-reload: adding a binding

Cedar policy is hot-reloaded when roles or bindings change. No restart needed.

```sh
kubectl apply -f - <<'EOF'
apiVersion: gcp.managed.openshift.io/v1
kind: RoleBinding
metadata:
  name: alice-viewer-org2
  namespace: org-2
spec:
  subject: alice@example.com
  roleRef:
    kind: PlatformRole
    name: cluster-viewer
    apiGroup: gcp.managed.openshift.io
EOF

# alice can now list clusters in org-2 -> 200
curl -s -w "\n%{http_code}" \
  -H "X-Endpoint-API-UserInfo: $(userinfo alice@example.com)" \
  $BASE/namespaces/org-2/clusters
```

## Expected results summary

| User | Namespace | Action | Expected |
|---|---|---|---|
| (no header) | org-1 | GET clusters | 401 |
| (malformed header) | org-1 | GET clusters | 401 |
| alice | org-1 | GET clusters | 200 |
| alice | org-2 | GET clusters | 403 |
| alice | org-1 | POST cluster | 403 |
| bob | org-2 | POST cluster | 201 |
| bob | org-1 | POST cluster | 403 |
| stranger | org-1 | GET clusters | 403 |

## Teardown

When you are done:

```sh
./deploy/kind/teardown.sh
```
