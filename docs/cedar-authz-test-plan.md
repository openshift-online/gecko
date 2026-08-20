# Cedar Authorization — Manual Test Plan

This document describes the manual test cases for gecko's Cedar-based authorization system. Each test case has an ID, description, prerequisites, steps, and expected result.

---

## Section 1: Authentication Middleware

### TC-AUTH-01: Valid X-Endpoint-API-UserInfo header

| Field | Value |
|---|---|
| **ID** | TC-AUTH-01 |
| **Description** | A valid base64-encoded JWT claims header is correctly parsed and the user is placed in the request context. |
| **Prerequisites** | Platform-api-server running in production mode (auth enabled). |
| **Steps** | 1. Construct JWT claims JSON: `{"email": "alice@example.com", "sub": "12345"}`. <br> 2. Base64-encode the JSON (raw URL encoding, no padding). <br> 3. Send a request with the header `X-Endpoint-API-UserInfo: <base64-value>`. <br> 4. Observe server logs or downstream handler output. |
| **Expected Result** | The request is authenticated. The user `alice@example.com` is present in the context. The request proceeds to the authz middleware (not rejected at authn). |

### TC-AUTH-02: Missing X-Endpoint-API-UserInfo header

| Field | Value |
|---|---|
| **ID** | TC-AUTH-02 |
| **Description** | A request without the authentication header is rejected. |
| **Prerequisites** | Platform-api-server running in production mode (auth enabled). |
| **Steps** | 1. Send a request to any API endpoint without the `X-Endpoint-API-UserInfo` header. |
| **Expected Result** | The server returns **401 Unauthorized**. |

### TC-AUTH-03: Malformed base64 in header

| Field | Value |
|---|---|
| **ID** | TC-AUTH-03 |
| **Description** | A header with invalid base64 content is rejected. |
| **Prerequisites** | Platform-api-server running in production mode (auth enabled). |
| **Steps** | 1. Send a request with `X-Endpoint-API-UserInfo: %%%not-valid-base64%%%`. |
| **Expected Result** | The server returns **401 Unauthorized**. |

### TC-AUTH-04: Valid base64 but no email claim

| Field | Value |
|---|---|
| **ID** | TC-AUTH-04 |
| **Description** | A properly encoded JWT that lacks the `email` field is rejected. |
| **Prerequisites** | Platform-api-server running in production mode (auth enabled). |
| **Steps** | 1. Construct JWT claims JSON without an email field: `{"sub": "12345", "name": "Alice"}`. <br> 2. Base64-encode the JSON (raw URL encoding, no padding). <br> 3. Send a request with `X-Endpoint-API-UserInfo: <base64-value>`. |
| **Expected Result** | The server returns **401 Unauthorized**. |

### TC-AUTH-05: Dev mode with X-Dev-User header

| Field | Value |
|---|---|
| **ID** | TC-AUTH-05 |
| **Description** | In dev mode, the X-Dev-User header is accepted as the user identity. |
| **Prerequisites** | Platform-api-server running with `--disable-auth` flag. |
| **Steps** | 1. Send a request with `X-Dev-User: dev-alice@example.com`. <br> 2. Observe server logs or downstream handler output. |
| **Expected Result** | The request is authenticated. The user `dev-alice@example.com` is present in the context. Authorization checks are also skipped entirely in dev mode. |

### TC-AUTH-06: Dev mode without X-Dev-User header

| Field | Value |
|---|---|
| **ID** | TC-AUTH-06 |
| **Description** | In dev mode, a request without the X-Dev-User header is rejected. |
| **Prerequisites** | Platform-api-server running with `--disable-auth` flag. |
| **Steps** | 1. Send a request without the `X-Dev-User` header. |
| **Expected Result** | The server returns **401 Unauthorized**. |

---

## Section 2: System PlatformRole Deployment

### TC-SEED-01: Deploy Helm chart creates system roles

| Field | Value |
|---|---|
| **ID** | TC-SEED-01 |
| **Description** | Deploying the platform-api-server Helm chart with `platformRoles.enabled=true` creates all system PlatformRoles in the database. |
| **Prerequisites** | Clean database with no existing system roles. Helm chart deployed with `platformRoles.enabled: true`. |
| **Steps** | 1. Deploy the Helm chart: `helm install platform-api-server ./helm/charts/platform-api-server --set platformRoles.enabled=true`. <br> 2. Wait for all resources to be applied. <br> 3. Query the database for PlatformRoles via kubectl: `kubectl get platformroles`. |
| **Expected Result** | All three system roles exist in the database: `cluster-viewer`, `cluster-admin`, and `service-admin`. Each has `spec.system: true` and the correct permissions (using singular dot notation, e.g., `cluster.get`, `role.list`). |

### TC-SEED-02: Update PlatformRole manifest permissions

| Field | Value |
|---|---|
| **ID** | TC-SEED-02 |
| **Description** | Modifying permissions in a PlatformRole manifest and redeploying updates the role in the database. |
| **Prerequisites** | System roles already deployed (TC-SEED-01 passed). |
| **Steps** | 1. Edit `helm/charts/platform-api-server/templates/platformroles/platformrole-cluster-viewer.yaml` to add `nodepool.update` to the permissions list. <br> 2. Upgrade the Helm release. <br> 3. Query the database for the `cluster-viewer` PlatformRole. |
| **Expected Result** | The `cluster-viewer` PlatformRole in the database has the updated permissions list including `nodepool.update`. |

### TC-SEED-03: Remove PlatformRole manifest

| Field | Value |
|---|---|
| **ID** | TC-SEED-03 |
| **Description** | Removing a PlatformRole manifest from the Helm chart and redeploying removes it from the database. |
| **Prerequisites** | System roles already deployed (TC-SEED-01 passed). |
| **Steps** | 1. Delete the file `helm/charts/platform-api-server/templates/platformroles/platformrole-cluster-viewer.yaml`. <br> 2. Upgrade the Helm release. <br> 3. Query the database for the `cluster-viewer` PlatformRole. |
| **Expected Result** | The `cluster-viewer` PlatformRole no longer exists in the database. Other system roles are unchanged. |

### TC-SEED-04: User-defined roles not affected by PlatformRole deployment

| Field | Value |
|---|---|
| **ID** | TC-SEED-04 |
| **Description** | Deploying or updating system PlatformRoles does not modify or delete namespace-scoped Roles created by users through the public API. |
| **Prerequisites** | System roles deployed. A user-defined Role `my-custom-role` exists in namespace `team-a` (created via the public API). |
| **Steps** | 1. Upgrade the Helm release (e.g., change a system role permission). <br> 2. Wait for the upgrade to complete. <br> 3. Query the database for `my-custom-role` in namespace `team-a`. |
| **Expected Result** | The `my-custom-role` Role is unchanged. Its permissions and metadata are exactly as they were before the Helm upgrade. |

### TC-SEED-05: Disable platformRoles.enabled

| Field | Value |
|---|---|
| **ID** | TC-SEED-05 |
| **Description** | Setting `platformRoles.enabled=false` prevents the Helm chart from managing PlatformRoles, allowing external management. |
| **Prerequisites** | None. |
| **Steps** | 1. Deploy the Helm chart with `platformRoles.enabled: false`. <br> 2. Verify no PlatformRole manifests are applied. <br> 3. Manually create a PlatformRole via kubectl. |
| **Expected Result** | The Helm chart does not create any PlatformRoles. Manually created PlatformRoles persist and function correctly. |

---

## Section 3: System Role Authorization

### TC-AUTHZ-01: cluster-admin can CRUD clusters

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-01 |
| **Description** | A user with the cluster-admin role can create, read, update, and delete clusters. |
| **Prerequisites** | User `admin@example.com` has a RoleBinding for `cluster-admin` (`roleRef.kind: PlatformRole`) in namespace `team-a`. |
| **Steps** | 1. As `admin@example.com`, send `POST /apis/.../namespaces/team-a/clusters` → should succeed. <br> 2. Send `GET /apis/.../namespaces/team-a/clusters` → should succeed. <br> 3. Send `GET /apis/.../namespaces/team-a/clusters/{id}` → should succeed. <br> 4. Send `PUT /apis/.../namespaces/team-a/clusters/{id}` → should succeed. <br> 5. Send `DELETE /apis/.../namespaces/team-a/clusters/{id}` → should succeed. |
| **Expected Result** | All five operations return success (2xx). None return 403. |

### TC-AUTHZ-02: cluster-admin gets 403 on rolebindings

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-02 |
| **Description** | The cluster-admin role does not grant access to manage rolebindings. |
| **Prerequisites** | User `admin@example.com` has a RoleBinding for `cluster-admin` in namespace `team-a` (and no other roles). |
| **Steps** | 1. As `admin@example.com`, send `GET /apis/.../namespaces/team-a/rolebindings`. <br> 2. Send `POST /apis/.../namespaces/team-a/rolebindings`. |
| **Expected Result** | Both requests return **403 Forbidden**. |

### TC-AUTHZ-03: cluster-viewer can read but not write clusters

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-03 |
| **Description** | The cluster-viewer role grants read access to clusters and nodepools but denies write operations. |
| **Prerequisites** | User `viewer@example.com` has a RoleBinding for `cluster-viewer` in namespace `team-a`. |
| **Steps** | 1. As `viewer@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should succeed. <br> 2. Send `GET /apis/.../namespaces/team-a/clusters/{id}` → should succeed. <br> 3. Send `POST /apis/.../namespaces/team-a/clusters` → should be denied. <br> 4. Send `PUT /apis/.../namespaces/team-a/clusters/{id}` → should be denied. <br> 5. Send `DELETE /apis/.../namespaces/team-a/clusters/{id}` → should be denied. |
| **Expected Result** | GET/LIST return 2xx. POST, PUT, DELETE return **403 Forbidden**. |

### TC-AUTHZ-04: service-admin can manage rolebindings and roles

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-04 |
| **Description** | The service-admin role grants full CRUD on roles and rolebindings within the namespace. |
| **Prerequisites** | User `svcadmin@example.com` has a RoleBinding for `service-admin` in namespace `team-a`. |
| **Steps** | 1. As `svcadmin@example.com`, send `POST /apis/.../namespaces/team-a/roles` → should succeed. <br> 2. Send `GET /apis/.../namespaces/team-a/roles` → should succeed. <br> 3. Send `PUT /apis/.../namespaces/team-a/roles/{name}` → should succeed. <br> 4. Send `DELETE /apis/.../namespaces/team-a/roles/{name}` → should succeed. <br> 5. Repeat steps 1–4 for `/apis/.../namespaces/team-a/rolebindings`. |
| **Expected Result** | All operations return 2xx. |

### TC-AUTHZ-05: service-admin gets 403 on clusters

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-05 |
| **Description** | The service-admin role does not grant access to cluster operations. |
| **Prerequisites** | User `svcadmin@example.com` has a RoleBinding for `service-admin` in namespace `team-a` (and no other roles). |
| **Steps** | 1. As `svcadmin@example.com`, send `GET /apis/.../namespaces/team-a/clusters`. <br> 2. Send `POST /apis/.../namespaces/team-a/clusters`. |
| **Expected Result** | Both requests return **403 Forbidden**. |

### TC-AUTHZ-06: No bindings means 403 on everything

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-06 |
| **Description** | A user with no role bindings is denied access to all resources. |
| **Prerequisites** | User `nobody@example.com` exists (can authenticate) but has zero RoleBindings. |
| **Steps** | 1. As `nobody@example.com`, send `GET /apis/.../namespaces/team-a/clusters`. <br> 2. Send `GET /apis/.../namespaces/team-a/roles`. <br> 3. Send `GET /apis/.../namespaces/team-a/rolebindings`. |
| **Expected Result** | All requests return **403 Forbidden**. |

### TC-AUTHZ-07: Cross-namespace list returns only authorized namespaces

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-07 |
| **Description** | A cross-namespace cluster list only returns clusters from namespaces the user is authorized in. |
| **Prerequisites** | User `viewer@example.com` has `cluster-viewer` in namespace `team-a` but NOT in `team-b`. Clusters exist in both namespaces. |
| **Steps** | 1. As `viewer@example.com`, send `GET /apis/.../clusters` (cross-namespace list, no namespace in path). |
| **Expected Result** | The response contains clusters from `team-a` only. No clusters from `team-b` appear. The response is 200 (not 403). |

### TC-AUTHZ-08: Cross-namespace list returns 403 for user with zero authorized namespaces

| Field | Value |
|---|---|
| **ID** | TC-AUTHZ-08 |
| **Description** | A cross-namespace list returns 403 (not an empty list) when the user has no authorized namespaces for the action. |
| **Prerequisites** | User `nobody@example.com` has zero RoleBindings. |
| **Steps** | 1. As `nobody@example.com`, send `GET /apis/.../clusters` (cross-namespace list). |
| **Expected Result** | The server returns **403 Forbidden**. The response is not an empty list. |

---

## Section 4: Validators

### TC-VAL-01: Role with invalid permission name is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-01 |
| **Description** | A role with an unrecognized permission string is rejected. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/roles` with permissions: `["invalid.permission", "also.fake"]`. |
| **Expected Result** | The request is rejected with a **400 Bad Request** indicating the permission name is not valid. |

### TC-VAL-02: Role with infra write permission is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-02 |
| **Description** | User-defined roles cannot include infrastructure write permissions. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/roles` with permissions: `["cluster.get", "cluster.create"]`. |
| **Expected Result** | The request is rejected with a **400 Bad Request** indicating `cluster.create` is not allowed in user-defined roles. |

### TC-VAL-03: Role with empty permissions is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-03 |
| **Description** | A role must have at least one permission. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/roles` with `"permissions": []`. |
| **Expected Result** | The request is rejected with a **400 Bad Request**. |

### TC-VAL-04: RoleBinding with unknown roleRef is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-04 |
| **Description** | Creating a RoleBinding that references a non-existent role is rejected. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. No role named `nonexistent-role` exists. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/rolebindings` with `roleRef.name: "nonexistent-role"`, `roleRef.kind: "Role"`, `roleRef.apiGroup: "gcp.managed.openshift.io"`. |
| **Expected Result** | The request is rejected with a **400 Bad Request** indicating the referenced role does not exist. |

### TC-VAL-05: Self-grant (subject == caller) is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-05 |
| **Description** | A user cannot create a RoleBinding where the subject is their own email. |
| **Prerequisites** | User `svcadmin@example.com` has `service-admin` in namespace `team-a`. |
| **Steps** | 1. As `svcadmin@example.com`, send `POST /apis/.../namespaces/team-a/rolebindings` with `subject: "svcadmin@example.com"`. |
| **Expected Result** | The request is rejected with an error indicating self-grant is not allowed. |

### TC-VAL-06: RoleBinding with invalid roleRef.kind is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-06 |
| **Description** | The roleRef.kind must be "PlatformRole" or "Role". |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/rolebindings` with `roleRef.kind: "ClusterRole"`. |
| **Expected Result** | The request is rejected with a **400 Bad Request** indicating the kind must be `"PlatformRole"` or `"Role"`. |

### TC-VAL-07: RoleBinding with invalid Cedar condition is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-07 |
| **Description** | A RoleBinding with syntactically invalid Cedar condition is rejected at creation time. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. A role `my-role` exists. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/rolebindings` with `condition: "context.spec.region = \"us-east1\""` (single `=` instead of `==`). |
| **Expected Result** | The request is rejected with a **400 Bad Request** indicating invalid Cedar syntax. |

### TC-VAL-08: RoleBinding with Namespace:: in condition is rejected

| Field | Value |
|---|---|
| **ID** | TC-VAL-08 |
| **Description** | Cedar conditions that reference Namespace:: entities are rejected. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. A role `my-role` exists. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/rolebindings` with `condition: "resource in Namespace::\"team-b\""`. |
| **Expected Result** | The request is rejected with a **400 Bad Request** indicating conditions must not reference `Namespace::`. |

---

## Section 5: User-Defined Roles

### TC-UDR-01: Create user-defined role with valid permissions

| Field | Value |
|---|---|
| **ID** | TC-UDR-01 |
| **Description** | A service-admin can create a user-defined role with allowed permissions. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/roles` with name `custom-reader` and permissions `["cluster.get", "cluster.list"]`. <br> 2. Send `GET /apis/.../namespaces/team-a/roles/custom-reader`. |
| **Expected Result** | The role is created successfully (201). The GET returns the role with the correct permissions. |

### TC-UDR-02: Create RoleBinding with Cedar condition, verify filtered access

| Field | Value |
|---|---|
| **ID** | TC-UDR-02 |
| **Description** | A RoleBinding with a Cedar condition restricts access by resource attributes. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. Role `cluster-reader` exists with `["cluster.get", "cluster.list"]`. User `newuser@example.com` has no existing bindings. Two clusters exist: `cluster-east` (spec.region=us-east1) and `cluster-west` (spec.region=us-west1). |
| **Steps** | 1. Create a RoleBinding: `POST /apis/.../namespaces/team-a/rolebindings` binding `newuser@example.com` to `cluster-reader` with `condition: "context.spec.region == \"us-east1\""`. <br> 2. As `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters/cluster-east` → should succeed. <br> 3. As `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters/cluster-west` → should be denied. <br> 4. As `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should return only `cluster-east`. |
| **Expected Result** | Step 2 returns 200. Step 3 returns 403. Step 4 returns a list containing only `cluster-east`. |

### TC-UDR-03: Bind user to role in namespace, verify access

| Field | Value |
|---|---|
| **ID** | TC-UDR-03 |
| **Description** | Binding a user to a custom role (no condition) grants the specified permissions. |
| **Prerequisites** | Role `custom-reader` exists in namespace `team-a` with permissions `["cluster.get", "cluster.list"]`. User `newuser@example.com` has no existing bindings. |
| **Steps** | 1. Create a RoleBinding: `POST /apis/.../namespaces/team-a/rolebindings` binding `newuser@example.com` to `custom-reader` (no condition). <br> 2. As `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should succeed. <br> 3. As `newuser@example.com`, send `POST /apis/.../namespaces/team-a/clusters` → should be denied. |
| **Expected Result** | Step 2 returns 200. Step 3 returns 403. |

### TC-UDR-04: User cannot access resources in other namespaces

| Field | Value |
|---|---|
| **ID** | TC-UDR-04 |
| **Description** | Namespace pinning ensures a binding in one namespace does not grant access to another. |
| **Prerequisites** | User `newuser@example.com` has `custom-reader` binding in namespace `team-a` only. Clusters exist in namespace `team-b`. |
| **Steps** | 1. As `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should succeed. <br> 2. As `newuser@example.com`, send `GET /apis/.../namespaces/team-b/clusters` → should be denied. |
| **Expected Result** | Step 1 returns 200. Step 2 returns 403. |

### TC-UDR-05: Delete custom role revokes access via hot-reload

| Field | Value |
|---|---|
| **ID** | TC-UDR-05 |
| **Description** | Deleting a user-defined role immediately revokes access for all users bound to it. |
| **Prerequisites** | Role `custom-reader` exists in namespace `team-a`. User `newuser@example.com` is bound to it and can access clusters. |
| **Steps** | 1. Verify access: as `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should succeed. <br> 2. Delete the role: `DELETE /apis/.../namespaces/team-a/roles/custom-reader`. <br> 3. Immediately retry: as `newuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters`. |
| **Expected Result** | Step 1 returns 200. Step 3 returns **403 Forbidden**. |

---

## Section 6: Hot-Reload

### TC-HOT-01: Create role after startup takes effect without restart

| Field | Value |
|---|---|
| **ID** | TC-HOT-01 |
| **Description** | A role created after the platform-api-server has started is recognized without restarting the server. |
| **Prerequisites** | Platform-api-server running. User `hotuser@example.com` has no bindings. |
| **Steps** | 1. As `hotuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should be denied (no bindings). <br> 2. Create a role `hot-role` with permission `cluster.get`, `cluster.list`. <br> 3. Create a RoleBinding binding `hotuser@example.com` to `hot-role` in `team-a`. <br> 4. As `hotuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` again (do NOT restart the server). |
| **Expected Result** | Step 1 returns 403. Step 4 returns 200. The new role and binding took effect without a restart. |

### TC-HOT-02: Delete binding removes access immediately

| Field | Value |
|---|---|
| **ID** | TC-HOT-02 |
| **Description** | Deleting a RoleBinding immediately revokes the user's access. |
| **Prerequisites** | User `hotuser@example.com` has a RoleBinding for `hot-role` in namespace `team-a` and can access clusters. |
| **Steps** | 1. As `hotuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters` → should succeed. <br> 2. Delete the RoleBinding for `hotuser@example.com`. <br> 3. Immediately retry: as `hotuser@example.com`, send `GET /apis/.../namespaces/team-a/clusters`. |
| **Expected Result** | Step 1 returns 200. Step 3 returns **403 Forbidden**. |

### TC-HOT-03: Update role permissions takes effect immediately

| Field | Value |
|---|---|
| **ID** | TC-HOT-03 |
| **Description** | Modifying a role's permissions is reflected immediately in authorization decisions. |
| **Prerequisites** | User `hotuser@example.com` is bound to `hot-role` in namespace `team-a`. The role currently has `cluster.get` and `cluster.list` permissions. |
| **Steps** | 1. As `hotuser@example.com`, send `GET /apis/.../namespaces/team-a/nodepools` → should be denied (no nodepool permissions). <br> 2. Update `hot-role` to add `nodepool.get` and `nodepool.list` permissions. <br> 3. Immediately retry: as `hotuser@example.com`, send `GET /apis/.../namespaces/team-a/nodepools`. |
| **Expected Result** | Step 1 returns 403. Step 3 returns 200. |

### TC-HOT-04: Role update invalidates cache for all users

| Field | Value |
|---|---|
| **ID** | TC-HOT-04 |
| **Description** | When a Role is modified, the entity cache for all users is invalidated (not just those bound to the role). |
| **Prerequisites** | Two users: `user-a@example.com` and `user-b@example.com`, each bound to different roles in `team-a`. |
| **Steps** | 1. Both users make requests to warm up their entity cache. <br> 2. Update the role bound to `user-a@example.com` to add a new permission. <br> 3. Verify `user-b@example.com` can still access resources normally. |
| **Expected Result** | After the role update both users' caches are invalidated and rebuilt correctly on the next request. `user-b@example.com` is unaffected by the cache invalidation (still authorized as before). |

---

## Section 7: Security Regressions

These test cases guard against specific bugs that were discovered during live testing. They must continue to pass.

### TC-SEC-01: Unconditional binding does not bypass conditioned binding for the same role

| Field | Value |
|---|---|
| **ID** | TC-SEC-01 |
| **Description** | When two users are bound to the same role — one with a condition and one without — the conditioned user's access must still be restricted by the condition. The unconditional binding must not bleed through to the conditioned user. |
| **Prerequisites** | Role `cluster-ro` exists in namespace `team-a` with `["cluster.get", "cluster.list"]`. <br> `user1@example.com` is bound to `cluster-ro` with **no condition** (can see all clusters). <br> `user2@example.com` is bound to `cluster-ro` with `condition: "context.resourceName == \"cluster-a\""`. <br> Two clusters exist: `cluster-a` and `cluster-b`. |
| **Steps** | 1. As `user1@example.com`, `GET /apis/.../namespaces/team-a/clusters/cluster-b` → should succeed (no condition). <br> 2. As `user2@example.com`, `GET /apis/.../namespaces/team-a/clusters/cluster-a` → should succeed (matches condition). <br> 3. As `user2@example.com`, `GET /apis/.../namespaces/team-a/clusters/cluster-b` → should be **denied** (fails condition). <br> 4. As `user2@example.com`, `GET /apis/.../namespaces/team-a/clusters` → should return **only `cluster-a`** in the list. |
| **Expected Result** | Steps 1 and 2 return 200. Step 3 returns **403 Forbidden**. Step 4 returns a list containing only `cluster-a`. |

### TC-SEC-02: User with role-X binding cannot access permissions of role-Y in the same namespace

| Field | Value |
|---|---|
| **ID** | TC-SEC-02 |
| **Description** | A user bound to `service-admin` (role/rolebinding permissions only) must not be able to access cluster resources, even though another user in the same namespace is bound to `cluster-admin`. |
| **Prerequisites** | Namespace `team-a`. <br> `svcadmin@example.com` has a RoleBinding for `service-admin` in `team-a`. <br> `clusteradmin@example.com` has a RoleBinding for `cluster-admin` in `team-a`. |
| **Steps** | 1. As `svcadmin@example.com`, send `GET /apis/.../namespaces/team-a/clusters`. <br> 2. As `svcadmin@example.com`, send `POST /apis/.../namespaces/team-a/clusters`. <br> 3. As `clusteradmin@example.com`, send `GET /apis/.../namespaces/team-a/roles`. |
| **Expected Result** | Steps 1 and 2 return **403 Forbidden** (service-admin has no cluster permissions). Step 3 returns **403 Forbidden** (cluster-admin has no role permissions). Neither user inherits the other's permissions by virtue of being in the same namespace. |

### TC-SEC-03: Invalid Cedar condition in RoleBinding is rejected before storage

| Field | Value |
|---|---|
| **ID** | TC-SEC-03 |
| **Description** | A RoleBinding with invalid Cedar syntax in `spec.condition` must be rejected at admission time. Storing an invalid condition would poison all subsequent policy reloads, leaving the authorizer frozen on a stale policy set. |
| **Prerequisites** | User has `service-admin` in namespace `team-a`. Role `my-role` exists. |
| **Steps** | 1. Send `POST /apis/.../namespaces/team-a/rolebindings` with `condition: "context.spec.region = \"us-east1\""` (single `=`). <br> 2. Verify no new binding was stored: `GET /apis/.../namespaces/team-a/rolebindings`. <br> 3. Send a normal authorized request to confirm the policy engine is still operational (not frozen on a stale set). |
| **Expected Result** | Step 1 returns **400 Bad Request**. Step 2 shows the binding was not created. Step 3 succeeds normally, confirming the policy engine is unaffected. |
