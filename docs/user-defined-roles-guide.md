# User-Defined Roles Guide

This guide explains how to create and manage user-defined roles in gecko. User-defined roles let you grant fine-grained, scoped access to users beyond the built-in system roles.

## What Are User-Defined Roles?

Gecko ships with three system roles (`cluster-viewer`, `cluster-admin`, `service-admin`) that cover common access patterns. User-defined roles let you create custom roles with a subset of permissions and optional Cedar conditions for attribute-based access control (ABAC).

**Use user-defined roles when you need to:**

- Grant access to a specific resource type without giving full cluster-admin.
- Restrict access by resource attributes (e.g., only clusters in a specific region).
- Create read-only roles with narrower scope than cluster-viewer.

## Prerequisites

- You must have the **service-admin** role in the target namespace (via a RoleBinding) — **only when authorization is enabled**. When using `--disable-auth`, all authorization checks are skipped and you cannot validate role enforcement in this mode.
- You need access to the gecko public API (port 8081).
- Authentication must be configured:
  - **Production**: ESPv2 with a valid JWT token
  - **Local development**: `--disable-auth` with `X-Dev-User` header (bypasses all auth checks — cannot validate role enforcement)

## Creating a User-Defined Role

### Basic role (no conditions)

```bash
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/my-namespace/roles \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "nodepool-viewer",
      "namespace": "my-namespace"
    },
    "spec": {
      "permissions": [
        "nodepool.get",
        "nodepool.list"
      ]
    }
  }'
```

Replace `$TOKEN` with a valid JWT token from your identity provider. For local development with `--disable-auth`, use `X-Endpoint-API-UserInfo: $(echo -n '{"email":"user@example.com"}' | base64)` instead.

### Role with a Cedar condition (ABAC)

Cedar conditions are placed on the **RoleBinding**, not on the Role itself. This allows the same Role to be bound with different conditions for different users. To use a condition, create the Role first, then create a RoleBinding with the `spec.condition` field set:

```bash
# 1. Create the role (no condition here)
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/my-namespace/roles \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "us-east1-cluster-reader",
      "namespace": "my-namespace"
    },
    "spec": {
      "permissions": [
        "cluster.get",
        "cluster.list"
      ]
    }
  }'

# 2. Bind a user with a Cedar condition on the RoleBinding
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/my-namespace/rolebindings \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "alice-us-east1-reader",
      "namespace": "my-namespace"
    },
    "spec": {
      "roleRef": {
        "kind": "Role",
        "name": "us-east1-cluster-reader",
        "apiGroup": "gcp.managed.openshift.io"
      },
      "subject": "alice@example.com",
      "condition": "context.spec.region == \"us-east1\""
    }
  }'
```

The condition is appended as a `when` clause to every permission in the role (alongside the namespace pin). For list operations the condition is evaluated **per item** — items that fail the condition are excluded from the response.

The resulting Cedar policy looks like:

```cedar
permit (
  principal,
  action in [Action::"GetCluster", Action::"ListClusters"],
  resource
)
when {
  principal in NamespaceRole::"my-namespace/us-east1-cluster-reader/alice-us-east1-reader" &&
  resource in Namespace::"my-namespace" &&
  (!(context has resourceName) || context.spec.region == "us-east1")
};
```

## Supported Permissions

The following table lists all 20 permissions in the system. The **User-Defined** column indicates whether the permission can be included in a user-defined role.

| Permission | Description | User-Defined |
|---|---|---|
| `cluster.get` | Get a single cluster | Yes |
| `cluster.list` | List clusters | Yes |
| `cluster.create` | Create a cluster | No |
| `cluster.update` | Update a cluster | No |
| `cluster.delete` | Delete a cluster | No |
| `nodepool.get` | Get a single node pool | Yes |
| `nodepool.list` | List node pools | Yes |
| `nodepool.create` | Create a node pool | No |
| `nodepool.update` | Update a node pool | No |
| `nodepool.delete` | Delete a node pool | No |
| `role.get` | Get a role | Yes |
| `role.list` | List roles | Yes |
| `role.create` | Create a role | Yes |
| `role.update` | Update a role | Yes |
| `role.delete` | Delete a role | Yes |
| `rolebinding.get` | Get a role binding | Yes |
| `rolebinding.list` | List role bindings | Yes |
| `rolebinding.create` | Create a role binding | Yes |
| `rolebinding.update` | Update a role binding | Yes |
| `rolebinding.delete` | Delete a role binding | Yes |

**Key restriction**: Infrastructure write permissions (`cluster.create`, `cluster.update`, `cluster.delete`, `nodepool.create`, `nodepool.update`, `nodepool.delete`) cannot be included in user-defined roles. This prevents privilege escalation through custom roles.

## Creating a RoleBinding

After creating a role, bind it to a user with a RoleBinding:

```bash
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/my-namespace/rolebindings \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "alice-nodepool-viewer",
      "namespace": "my-namespace"
    },
    "spec": {
      "roleRef": {
        "kind": "Role",
        "name": "nodepool-viewer",
        "apiGroup": "gcp.managed.openshift.io"
      },
      "subject": "alice@example.com"
    }
  }'
```

The binding takes effect immediately (via hot-reload) — no server restart required.

**Note**: You cannot create a binding where the subject is your own email. This prevents self-grant of permissions.

## Cedar Condition Syntax

Cedar conditions are stored on the **RoleBinding** (`spec.condition`), not on the Role. The condition is evaluated at authorization time and has access to the `context` record, which contains resource attributes.

### Available context attributes

| Attribute | Type | Description |
|---|---|---|
| `context.resourceName` | string | The resource name (absent during list authorization; present per-item) |
| `context.resourcePlural` | string | Resource kind plural (`clusters`, `nodepools`, etc.) |
| `context.method` | string | HTTP method |
| `context.spec.*` | record | Full nested spec from the resource object |

For resource-specific attributes (e.g., `region`), access them via `context.spec.region`. The spec fields available depend on the resource type.

### Condition examples

**Single attribute match:**
```cedar
context.spec.region == "us-east1"
```

**Multiple conditions (AND):**
```cedar
context.spec.region == "us-east1" && context.resourceName like "prod-*"
```

**Set membership:**
```cedar
["us-east1", "us-west1"].contains(context.spec.region)
```

### Condition restrictions

- **No `Namespace::` references**: Conditions must not reference the `Namespace::` entity type. Namespace scoping is handled by RoleBindings, not conditions.
- **Valid Cedar syntax required**: The condition must be syntactically valid Cedar. Invalid syntax is rejected at RoleBinding creation time.

## Limitations

1. **No infrastructure write permissions**: User-defined roles cannot include `cluster.create/update/delete` or `nodepool.create/update/delete`.
2. **Namespace-scoped only**: User-defined roles are always namespaced. They must be bound via RoleBinding.
3. **Conditions live on the RoleBinding**: The `spec.condition` field is on the RoleBinding, not on the Role. A single Role can be bound with different conditions to different users.
4. **Conditions apply to all permissions in the role**: A single condition covers every permission in the bound role. Create separate roles (and bindings) if you need different conditions for different permissions.
5. **No `Namespace::` in conditions**: Cedar conditions must not reference `Namespace::` entity types.

## Examples

### Read-only access to us-east1 clusters

Create a role that allows reading clusters, then bind a user with a region condition:

```bash
# Create the role
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/team-alpha/roles \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "cluster-reader",
      "namespace": "team-alpha"
    },
    "spec": {
      "permissions": ["cluster.get", "cluster.list"]
    }
  }'

# Bind to a user with a region condition
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/team-alpha/rolebindings \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "bob-us-east1-reader",
      "namespace": "team-alpha"
    },
    "spec": {
      "roleRef": {
        "kind": "Role",
        "name": "cluster-reader",
        "apiGroup": "gcp.managed.openshift.io"
      },
      "subject": "bob@example.com",
      "condition": "context.spec.region == \"us-east1\""
    }
  }'
```

Bob can now `GET /apis/.../namespaces/team-alpha/clusters/{id}` for clusters in us-east1, and listing clusters will return only us-east1 clusters.

### Node pool viewer

A role that only allows viewing node pools (no cluster access):

```bash
curl -X POST https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/team-alpha/roles \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "nodepool-viewer",
      "namespace": "team-alpha"
    },
    "spec": {
      "permissions": ["nodepool.get", "nodepool.list"]
    }
  }'
```

## Updating a User-Defined Role

```bash
curl -X PUT https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/my-namespace/roles/nodepool-viewer \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {
      "name": "nodepool-viewer",
      "namespace": "my-namespace"
    },
    "spec": {
      "permissions": [
        "nodepool.get",
        "nodepool.list",
        "cluster.get"
      ]
    }
  }'
```

Changes take effect immediately via hot-reload. Note that updating a Role invalidates the **entire entity cache** (all users), not just those bound to this role.

## Deleting a User-Defined Role

```bash
curl -X DELETE https://gecko-api.example.com/apis/gcp.managed.openshift.io/v1/namespaces/my-namespace/roles/nodepool-viewer \
  -H "Authorization: Bearer $TOKEN"
```

After deletion:
- All existing RoleBindings referencing this role become ineffective.
- Users who had access through this role lose it immediately (hot-reload).

## Troubleshooting

### 403 Forbidden on all requests

**Possible causes:**
- You have no RoleBinding in the target namespace. Check your bindings: `GET /apis/.../namespaces/{ns}/rolebindings`.
- Your role has no permissions for the resource type you are accessing.
- If your RoleBinding has a Cedar condition, the condition is filtering out the resource. Try removing the condition from the RoleBinding to verify.
- For cross-namespace list requests (no namespace in path), the server returns 403 (not an empty list) if you have zero authorized namespaces.

### 400 Bad Request: invalid Cedar condition syntax

The `spec.condition` field on the RoleBinding must contain valid Cedar expression syntax. Common mistakes:
- Missing quotes around string values: use `context.spec.region == "us-east1"`, not `context.spec.region == us-east1`.
- Using `=` instead of `==` for comparison.
- Using `&&` or `||` without valid expressions on both sides.

### 400 Bad Request: roleRef not found

The `roleRef.name` in your RoleBinding must reference an existing role. Lookup rules depend on `roleRef.kind`:

**For `roleRef.kind: "Role"` (namespace-scoped user-defined roles):**
- The role must exist in the **same namespace as the RoleBinding**.
- Check: `GET /apis/.../namespaces/{ns}/roles/{name}`.
- The role name must match exactly (case-sensitive).

**For `roleRef.kind: "PlatformRole"` (cluster-scoped system roles):**
- The role is looked up cluster-wide, **not in the RoleBinding's namespace**.
- Check: `GET /apis/.../platformroles.gcp.managed.openshift.io/{name}`.
- The role name must match exactly (case-sensitive).
- The RoleBinding can be in any namespace.

In both cases:
- `roleRef.apiGroup` must be set to `"gcp.managed.openshift.io"`.

### 400 Bad Request: permission not allowed in user-defined role

You are trying to include a restricted permission (e.g., `cluster.create`) in a user-defined role. Review the permissions table above to see which permissions are allowed.

### 400 Bad Request: condition must not reference Namespace

Your Cedar condition contains a reference to `Namespace::`. Namespace scoping is handled by the RoleBinding, not by conditions. Remove any `Namespace::` references from your condition.

### 403 Forbidden: cannot create role (need service-admin)

You need the `service-admin` role in the target namespace to create or manage user-defined roles and bindings. Ask an existing service-admin to grant you a service-admin RoleBinding.
