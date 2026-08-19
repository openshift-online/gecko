package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/handlers"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// maxAuthzBodyBytes is the maximum size of a request body to read for authorization context.
	// This prevents memory exhaustion attacks via arbitrarily large POST/PUT bodies.
	maxAuthzBodyBytes = 10 * 1024 * 1024 // 10 MB
)

// Middleware returns HTTP middleware that enforces Cedar authorization.
//
// The middleware parses the request URL path to determine the action,
// namespace, and resource. It supports the following URL patterns:
//
// Namespaced resources:
//
//	GET    /apis/{group}/{version}/namespaces/{namespace}/{plural}           -> namespaced list
//	GET    /apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}    -> get
//	POST   /apis/{group}/{version}/namespaces/{namespace}/{plural}           -> create
//	PUT    /apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}    -> update
//	DELETE /apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}    -> delete
//
// Cross-namespace list (no namespace in URL):
//
//	GET    /apis/{group}/{version}/{plural}                                  -> cross-namespace list
func Middleware(authorizer *Authorizer, disableAuth bool) func(http.Handler) http.Handler {
	if disableAuth {
		log.Println("WARNING: Authorization is disabled. This mode is for local development only. " +
			"Do not enable in production or untrusted environments.")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if disableAuth {
				next.ServeHTTP(w, r)
				return
			}

			// Extract the authenticated user.
			user, ok := authn.UserFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthorized: no user in context", http.StatusUnauthorized)
				return
			}

		// Derive action from the URL path.
		action, namespace, isCrossNsList := deriveAction(r)
		if action == "" {
			// No matching route pattern — deny the request as unauthorized.
			// This prevents unknown routes from being silently allowed.
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

			ctx := r.Context()

			// Cross-namespace list: get authorized namespaces and inject item filter.
			if isCrossNsList {
				parsed, _ := parseURLPath(r.URL.Path)
				namespaces, err := authorizer.AuthorizedNamespaces(ctx, user, action)
				if err != nil {
					log.Printf("authz: error getting authorized namespaces for %q: %v", user, err)
					http.Error(w, "internal authorization error", http.StatusInternalServerError)
					return
				}
				if len(namespaces) == 0 {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				ctx = handlers.WithAuthorizedNamespaces(ctx, namespaces)

				// Inject item filter for per-item condition evaluation.
				itemFilter := buildItemFilter(authorizer, user, action, parsed.plural)
				ctx = handlers.WithItemFilter(ctx, itemFilter)

				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Build Cedar context for this specific request.
			parsed, _ := parseURLPath(r.URL.Path)

			isList := r.Method == http.MethodGet && parsed.name == "" && namespace != ""

			if isList {
				// For namespaced list operations, authorize at namespace level without
				// Cedar context (conditions don't apply to the list as a whole — they
				// are evaluated per-item by the ItemFilter). This allows users with
				// conditioned bindings to list and receive filtered results.
				allowed, err := authorizer.Authorize(ctx, user, action, namespace)
				if err != nil {
					log.Printf("authz: error authorizing %q for %q: %v", user, action, err)
					http.Error(w, "internal authorization error", http.StatusInternalServerError)
					return
				}
				if !allowed {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				// Inject item filter for per-item condition evaluation.
				itemFilter := buildItemFilter(authorizer, user, action, parsed.plural)
				ctx = handlers.WithItemFilter(ctx, itemFilter)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

		// For non-list operations (single resource GET, POST, PUT, DELETE):
		// build Cedar context with resource attributes and apply conditions.
		cedarCtx, bodyBytes, err := buildCedarContext(r, parsed)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// For writes with a body, restore it so the handler can re-read it.
		if bodyBytes != nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		allowed, err := authorizer.AuthorizeWithContext(ctx, user, action, namespace, cedarCtx)
			if err != nil {
				log.Printf("authz: error authorizing %q for %q: %v", user, action, err)
				http.Error(w, "internal authorization error", http.StatusInternalServerError)
				return
			}

			if !allowed {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// buildItemFilter creates an ItemFilterFunc that evaluates each list item
// against Cedar with the item's attributes in the context record.
func buildItemFilter(authorizer *Authorizer, user, action, plural string) handlers.ItemFilterFunc {
	return func(ctx context.Context, obj runtime.Object) bool {
		// Build Cedar context from the item's attributes.
		cedarCtx := buildCedarContextFromObject(obj, plural)

		// Derive namespace from the object metadata.
		type metaAccessor interface {
			GetNamespace() string
		}
		namespace := ""
		if m, ok := obj.(metaAccessor); ok {
			namespace = m.GetNamespace()
		}

		allowed, err := authorizer.AuthorizeWithContext(ctx, user, action, namespace, cedarCtx)
		if err != nil {
			log.Printf("authz: item filter error for %q: %v", user, err)
			return false
		}
		return allowed
	}
}

// buildCedarContext builds a Cedar Record from the HTTP request containing
// resource attributes for condition evaluation.
//
// Returns the Cedar record, the raw body bytes (if body was read, so caller
// can restore r.Body), and an error if the body read fails or exceeds size limit.
func buildCedarContext(r *http.Request, parsed parsedRoute) (cedar.Record, []byte, error) {
	rm := cedar.RecordMap{
		cedar.String("resourceName"):   cedar.String(parsed.name),
		cedar.String("resourcePlural"): cedar.String(parsed.plural),
		cedar.String("method"):         cedar.String(r.Method),
	}

	// For write operations (POST/PUT/PATCH) or any request with a body, parse it.
	var bodyBytes []byte
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
		// Limit the body read to prevent memory exhaustion.
		limitedReader := io.LimitReader(r.Body, maxAuthzBodyBytes)
		var err error
		bodyBytes, err = io.ReadAll(limitedReader)
		if err != nil {
			return cedar.NewRecord(rm), nil, fmt.Errorf("read request body: %w", err)
		}
		if len(bodyBytes) > 0 {
			var obj map[string]interface{}
			if json.Unmarshal(bodyBytes, &obj) == nil {
				// Extract name from metadata if not in URL.
				if parsed.name == "" {
					if meta, ok := obj["metadata"].(map[string]interface{}); ok {
						if name, ok := meta["name"].(string); ok {
							rm[cedar.String("resourceName")] = cedar.String(name)
						}
					}
				}
				// Add spec fields to context.
				if spec, ok := obj["spec"].(map[string]interface{}); ok {
					rm[cedar.String("spec")] = mapToCedarRecord(spec)
				}
			}
		}
	}

	return cedar.NewRecord(rm), bodyBytes, nil
}

// buildCedarContextFromObject builds a Cedar Record from a runtime.Object's
// JSON representation, for per-item filtering in list operations.
func buildCedarContextFromObject(obj runtime.Object, plural string) cedar.Record {
	// JSON-marshal the object to a generic map for attribute extraction.
	type metaAccessor interface {
		GetName() string
	}

	rm := cedar.RecordMap{
		cedar.String("resourcePlural"): cedar.String(plural),
		cedar.String("method"):         cedar.String(http.MethodGet),
	}

	if m, ok := obj.(metaAccessor); ok {
		rm[cedar.String("resourceName")] = cedar.String(m.GetName())
	}

	// Marshal the object to JSON and extract the spec field.
	data, err := json.Marshal(obj)
	if err == nil {
		var generic map[string]interface{}
		if json.Unmarshal(data, &generic) == nil {
			if spec, ok := generic["spec"].(map[string]interface{}); ok {
				rm[cedar.String("spec")] = mapToCedarRecord(spec)
			}
		}
	}

	return cedar.NewRecord(rm)
}

// mapToCedarRecord recursively converts a map[string]interface{} to a cedar.Value.
// Nested maps become cedar.Record, slices become cedar.Set, strings become cedar.String,
// bools become cedar.Boolean, numbers become cedar.Long (integer) or cedar.Decimal.
func mapToCedarRecord(m map[string]interface{}) cedar.Value {
	rm := cedar.RecordMap{}
	for k, v := range m {
		rm[cedar.String(k)] = anyToCedar(v)
	}
	return cedar.NewRecord(rm)
}

func anyToCedar(v interface{}) cedar.Value {
	if v == nil {
		return cedar.String("")
	}
	switch val := v.(type) {
	case string:
		return cedar.String(val)
	case bool:
		return cedar.Boolean(val)
	case float64:
		return cedar.Long(int64(val))
	case map[string]interface{}:
		rm := cedar.RecordMap{}
		for k, vv := range val {
			rm[cedar.String(k)] = anyToCedar(vv)
		}
		return cedar.NewRecord(rm)
	case []interface{}:
		elems := make([]cedar.Value, 0, len(val))
		for _, elem := range val {
			elems = append(elems, anyToCedar(elem))
		}
		return cedar.NewSet(elems...)
	default:
		return cedar.String("")
	}
}

// parsedRoute holds the components extracted from a URL path.
type parsedRoute struct {
	plural    string
	namespace string
	name      string
}

// parseURLPath parses a URL path into its route components.
func parseURLPath(urlPath string) (parsedRoute, bool) {
	// Canonicalize the path to prevent traversal attacks (e.g., //../../).
	// Reject non-canonical paths that change when cleaned.
	cleaned := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	// Compare accounting for optional trailing slash.
	pathWithoutTrailing := strings.TrimSuffix(urlPath, "/")
	cleanedWithoutTrailing := strings.TrimSuffix(cleaned, "/")
	if cleanedWithoutTrailing != pathWithoutTrailing && cleaned != urlPath {
		return parsedRoute{}, false
	}

	trimmed := strings.Trim(urlPath, "/")
	parts := strings.Split(trimmed, "/")

	if len(parts) < 4 || parts[0] != "apis" {
		return parsedRoute{}, false
	}

	if len(parts) >= 6 && parts[3] == "namespaces" {
		r := parsedRoute{
			namespace: parts[4],
			plural:    parts[5],
		}
		if len(parts) >= 7 {
			r.name = parts[6]
		}
		return r, true
	}

	r := parsedRoute{
		plural: parts[3],
	}
	if len(parts) >= 5 {
		r.name = parts[4]
	}
	return r, true
}

// deriveAction determines the Cedar action, namespace, and whether this is a
// cross-namespace list from the HTTP request.
func deriveAction(r *http.Request) (action string, namespace string, isCrossNsList bool) {
	parsed, ok := parseURLPath(r.URL.Path)
	if !ok {
		return "", "", false
	}

	method := r.Method
	action = resolveAction(parsed.plural, method, parsed.name)
	if action == "" {
		return "", "", false
	}

	if parsed.namespace == "" && method == http.MethodGet && parsed.name == "" {
		return action, "", true
	}

	return action, parsed.namespace, false
}

// resolveAction maps the plural resource name, HTTP method, and whether a name
// is present to a Cedar action.
func resolveAction(plural, method, name string) string {
	if name != "" && method == http.MethodGet {
		if action, ok := ResourceSingularGetAction[plural]; ok {
			return action
		}
	}

	actions, ok := ResourcePluralToActions[plural]
	if !ok {
		return ""
	}

	return actions[method]
}


