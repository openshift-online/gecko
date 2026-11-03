# PlatformRoleBinding POC

This POC authorizes Version and Channel reads through the existing role-binding
mechanism, extended to platform scope. It is an alternative to the embedded,
named-policy implementation in [Gecko #447](https://github.com/openshift-online/gecko/pull/447).

## Contract

* PlatformRoleBindings are private, platform-scoped resources with the same
  `subject`, `roleRef`, and optional Cedar `condition` shape as RoleBindings.
  They reference PlatformRoles and grant only platform-scoped permissions.
* Namespace RoleBindings retain their namespace boundary and exact-subject
  semantics. A role may contain both scopes' permissions; only those applicable
  to the binding's scope take effect.
* `subject: "*"` selects all authenticated callers in a PlatformRoleBinding.
  Exact normalized email subjects also work. Further restrictions use Cedar
  conditions rather than another subject-pattern language.
* Conditions are supported, including public-object evaluation and list
  filtering before pagination. The initial binding deliberately omits its
  condition. Additional principal attributes are needed only when a configured
  condition uses them.
* Both binding kinds use one authorizer, per-binding policy generation, and the
  existing entity-cache/watch/reload lifecycle. Failed refreshes retain the
  last-known-good policy set, following existing RoleBinding behavior.
* Registered resource scope and the existing permission mapping drive request
  authorization. This POC removes authorization exemptions and introduces no
  additional Orlop authorization annotations. The existing public authorization
  feature flag remains unchanged.

## Run the self-contained POC

From the repository root, using the documented Go toolchain:

```sh
GOTOOLCHAIN=go1.26.4 go -C platform-api test ./cmd/platform-api-server \
  -run '^TestPublicAPICatalogBindingAuthorization$' -count=1 -v

GOTOOLCHAIN=go1.26.4 go -C platform-api test ./pkg/authz ./api/private/v1 \
  -run 'Platform|RolesAcceptMixedScope' -count=1
```

The HTTP test starts real private and public API listeners on loopback with
in-memory storage. Private authentication is disabled only for this local test
harness; public authentication and Cedar authorization remain active. It uses
test identity headers directly on the local backend. Production public access
retains the existing ESPv2 trust boundary. The test needs no Kubernetes cluster,
cloud credentials, database service, or deployed Argo CD instance.

The tests exercise:

1. Denial of authenticated catalog reads before a grant exists, and `401` for
   unauthenticated public requests.
2. Private discovery and creation of a PlatformRoleBinding; exclusion of the
   binding from public discovery and public CRUD.
3. The unconditional authenticated-user grant for Version/Channel get/list,
   with writes, watches, namespace resources, and cross-namespace requests
   remaining denied.
4. Cedar conditions on channel names and Version `spec.channelGroups`, public
   field filtering, and authorization before list pagination.
5. An unsatisfied principal condition yielding an empty collection while the
   corresponding item read is denied.
6. Exact-subject matching, mixed-permission scope isolation, independent
   conditioned grants, malformed bindings, and safe authorization error logs.
7. Binding updates/deletion and referenced-role deletion through existing
   watches, plus last-known-good behavior during a failed refresh.

## GitOps configuration

The two example manifests define the initial grant:

* [authenticated-user.yaml](authenticated-user.yaml): the four catalog-read
  permissions, with an ordinary role name rather than special engine behavior.
* [authenticated-users.yaml](authenticated-users.yaml): the all-authenticated
  binding, with no condition.

These are example inputs for the existing private-API/Argo CD workflow, not
automatically installed grants. A deployment needs the matching POC API server,
the role applied before its binding, and platform-owned catalog resources.
Production integration belongs in `gcp-hcp-infra`: Argo CD applies the resources,
and private Kubernetes RBAC reserves binding mutation for the Argo CD identity.
This POC leaves infrastructure RBAC and production deployment configuration
untouched; the self-contained test does not validate those deployment controls.

PlatformRole public discovery remains separate work. This POC covers only
Version and Channel public get/list operations.
