package authz

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"
)

// Middleware returns HTTP middleware that enforces Cedar-based authorization.
// It reads the user email from context (set by authn middleware), derives the
// Cedar action and resource from the HTTP method and chi route pattern, and
// evaluates authorization against the Cedar policy set.
func NewMiddleware(authorizer *Authorizer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip authz for health/readiness probes
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}

			email := authn.UserFromContext(r.Context())
			if email == "" {
				writeAuthzError(w, http.StatusUnauthorized, "Unauthenticated")
				return
			}

			// Derive Cedar action and resource from the request
			action, resourceType, resourceID, isCrossNsList := deriveActionAndResource(r)
			// Log authorization decisions for debugging
			rctx := chi.RouteContext(r.Context())
			routePattern := ""
			if rctx != nil {
				routePattern = rctx.RoutePattern()
			}
			log.Printf("authz: email=%s method=%s path=%s routePattern=%q action=%s resourceType=%s resourceID=%s crossNS=%v",
				email, r.Method, r.URL.Path, routePattern, action, resourceType, resourceID, isCrossNsList)
			if action == "" {
				// Could not determine action — allow the request through
				// (e.g., health checks, discovery endpoints)
				next.ServeHTTP(w, r)
				return
			}

			// Handle cross-namespace list: pre-compute authorized namespace set
			if isCrossNsList {
				namespaces, err := authorizer.entityGetter.AuthorizedNamespaces(r.Context(), email)
				if err != nil {
					writeAuthzError(w, http.StatusInternalServerError, "Authorization error")
					return
				}
				// Inject authorized namespaces into context for the handler
				ctx := storage.ContextWithAuthorizedNamespaces(r.Context(), namespaces)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Single-resource authorization check
			decision, err := authorizer.Authorize(r.Context(), email, action, resourceType, resourceID)
			if err != nil {
				writeAuthzError(w, http.StatusInternalServerError, "Authorization error")
				return
			}

			if decision != Allow {
				writeAuthzError(w, http.StatusForbidden, "Forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ActionMapping maps (HTTP method, resource kind) to Cedar action names.
type ActionMapping struct {
	method   string
	isList   bool // true if the route is for listing (no {name} param)
	isCreate bool // true if the route is POST (create)
}

// deriveActionAndResource extracts the Cedar action name and resource identity
// from the HTTP request by parsing the URL path.
//
// URL patterns:
//   /apis/{group}/{version}/namespaces/{ns}/{plural}           -> List (namespaced)
//   /apis/{group}/{version}/namespaces/{ns}/{plural}/{name}    -> Get/Update/Delete
//   /apis/{group}/{version}/{plural}                           -> List (cross-ns or cluster-scoped)
//   /apis/{group}/{version}/{plural}/{name}                    -> Get/Update/Delete (cluster-scoped)
//
// Returns:
//   - action: Cedar action name (e.g., "ListClusters")
//   - resourceType: Cedar entity type (e.g., "Gecko::Namespace")
//   - resourceID: Cedar entity ID (e.g., "ns" or "ns/name")
//   - isCrossNsList: true if this is a cross-namespace list
func deriveActionAndResource(r *http.Request) (action, resourceType, resourceID string, isCrossNsList bool) {
	path := r.URL.Path
	method := r.Method

	// Only handle API paths
	if !strings.HasPrefix(path, "/apis/") {
		return "", "", "", false
	}

	// Strip /apis/{group}/{version}/ prefix
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	// parts[0]="apis", parts[1]=group, parts[2]=version, parts[3+]=resource path
	if len(parts) < 4 {
		return "", "", "", false
	}
	resourcePath := parts[3:] // everything after /apis/group/version/

	var namespace, plural, name string
	isStatusUpdate := false

	if len(resourcePath) >= 1 && resourcePath[0] == "namespaces" {
		// Namespaced: /namespaces/{ns}/{plural}[/{name}[/status]]
		if len(resourcePath) < 3 {
			return "", "", "", false
		}
		namespace = resourcePath[1]
		plural = resourcePath[2]
		if len(resourcePath) >= 4 {
			name = resourcePath[3]
		}
		if len(resourcePath) >= 5 && resourcePath[4] == "status" {
			isStatusUpdate = true
		}
		// Handle parent routes: /namespaces/{ns}/{parentPlural}/{parentID}/{childPlural}[/{name}]
		if len(resourcePath) >= 5 && resourcePath[4] != "status" {
			plural = resourcePath[4]
			name = ""
			if len(resourcePath) >= 6 {
				name = resourcePath[5]
			}
		}
	} else {
		// Cluster-scoped or cross-namespace list: /{plural}[/{name}[/status]]
		plural = resourcePath[0]
		if len(resourcePath) >= 2 {
			name = resourcePath[1]
		}
		if len(resourcePath) >= 3 && resourcePath[2] == "status" {
			isStatusUpdate = true
		}
	}

	resourceKind := pluralToKind(plural)
	if resourceKind == "" {
		return "", "", "", false
	}

	isNamespacedResource := isNamespacedKind(resourceKind)

	// Cross-namespace list detection
	if isNamespacedResource && namespace == "" && method == "GET" {
		action = listAction(resourceKind)
		return action, "", "", true
	}

	// Derive Cedar action
	switch method {
	case "GET":
		if name == "" {
			action = listAction(resourceKind)
		} else {
			action = getAction(resourceKind)
		}
	case "POST":
		action = createAction(resourceKind)
	case "PUT", "PATCH":
		if isStatusUpdate {
			action = updateAction(resourceKind)
		} else {
			action = updateAction(resourceKind)
		}
	case "DELETE":
		action = deleteAction(resourceKind)
	default:
		return "", "", "", false
	}

	// Derive Cedar resource
	if isNamespacedResource {
		if name != "" {
			resourceType = entityTypeForKind(resourceKind)
			resourceID = namespace + "/" + name
		} else {
			resourceType = TypeNamespace
			resourceID = namespace
		}
	} else {
		if name != "" {
			resourceType = entityTypeForKind(resourceKind)
			resourceID = name
		} else {
			resourceType = TypePlatform
			resourceID = PlatformEntity
		}
	}

	return action, resourceType, resourceID, false
}

// pluralToKind maps plural resource names to their Cedar kind names.
func pluralToKind(plural string) string {
	switch plural {
	case "clusters":
		return "Cluster"
	case "nodepools":
		return "Nodepool"
	case "rolebindings":
		return "RoleBinding"
	case "platformrolebindings":
		return "PlatformRoleBinding"
	case "customroles":
		return "CustomRole"
	default:
		return ""
	}
}

// isNamespacedKind returns true for namespace-scoped resource kinds.
func isNamespacedKind(kind string) bool {
	switch kind {
	case "Cluster", "Nodepool", "RoleBinding", "CustomRole":
		return true
	case "PlatformRoleBinding":
		return false
	default:
		return false
	}
}

// entityTypeForKind maps resource kind to Cedar entity type.
func entityTypeForKind(kind string) string {
	switch kind {
	case "Cluster":
		return TypeCluster
	case "Nodepool":
		return TypeNodePool
	default:
		// For RoleBinding, PlatformRoleBinding, CustomRole, we use the namespace/platform
		// as the resource since authorization is checked at the scope level.
		return TypeNamespace
	}
}

// Action name constructors matching PermissionToAction output format.
// These must be consistent with the Cedar actions generated from permissions.
func createAction(kind string) string { return "Create" + kind }
func listAction(kind string) string   { return "List" + pluralKind(kind) }
func getAction(kind string) string    { return "Get" + kind }
func updateAction(kind string) string { return "Update" + kind }
func deleteAction(kind string) string { return "Delete" + kind }

// pluralKind returns the plural form of a resource kind for list actions.
func pluralKind(kind string) string {
	switch kind {
	case "Cluster":
		return "Clusters"
	case "Nodepool":
		return "Nodepools"
	case "RoleBinding":
		return "RoleBindings"
	case "PlatformRoleBinding":
		return "PlatformRoleBindings"
	case "CustomRole":
		return "CustomRoles"
	default:
		return kind + "s"
	}
}

func writeAuthzError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":    "Status",
		"status":  "Failure",
		"message": message,
		"code":    code,
	})
}
