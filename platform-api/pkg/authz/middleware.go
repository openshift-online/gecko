package authz

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/go-logr/logr"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/types"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"
)

// Middleware enforces Cedar authorization for public API operations.
func Middleware(authorizer *Authorizer, logger logr.Logger, resources []types.ResourceInfo) func(http.Handler) http.Handler {
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMetadataPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			email, ok := authn.UserFromContext(r.Context())
			if !ok {
				writeForbidden(w)
				return
			}
			if authorizer == nil {
				logger.Error(fmt.Errorf("authorizer is not configured"), "authorization denied")
				writeForbidden(w)
				return
			}

			requestInfo, err := parseRequestPath(r.URL.Path)
			if err != nil {
				logger.Info("authorization denied for invalid public API path", "method", r.Method, "error", err)
				writeForbidden(w)
				return
			}
			if r.URL.Query().Get("watch") == "true" {
				logger.Info("authorization denied for unsupported watch operation", "resource", requestInfo.plural)
				writeForbidden(w)
				return
			}
			resource, found := resourceForRequest(requestInfo, resources)
			if !found {
				logger.Info("authorization denied for unrecognized public API resource", "resource", requestInfo.plural)
				writeForbidden(w)
				return
			}
			verb, err := verbForRequest(r.Method, requestInfo.plural, requestInfo.named)
			if err != nil {
				logger.Info("authorization denied for unsupported public API operation", "method", r.Method, "resource", requestInfo.plural)
				writeForbidden(w)
				return
			}
			if !resource.VerbAllowed(verb) {
				logger.Info("authorization denied for public API verb", "verb", verb, "resource", requestInfo.plural)
				writeForbidden(w)
				return
			}
			action, err := actionForRequest(r.Method, requestInfo.plural, requestInfo.named)
			if err != nil {
				logger.Info("authorization denied for unsupported public API operation", "method", r.Method, "resource", requestInfo.plural)
				writeForbidden(w)
				return
			}

			var allowed bool
			if resource.Namespaced {
				if requestInfo.namespace == "" {
					// Cross-namespace filtering is not implemented in the namespace
					// authorization path.
					writeForbidden(w)
					return
				}
				allowed, err = authorizer.Authorize(r.Context(), email, action, requestInfo.namespace)
			} else {
				allowed, err = authorizer.AuthorizePlatform(r.Context(), email, action, resource, verb, requestInfo.name)
			}
			if err != nil {
				logger.Error(err, "authorization evaluation failed closed", "method", r.Method, "resource", requestInfo.plural, "namespace", requestInfo.namespace)
				writeForbidden(w)
				return
			}
			if !allowed {
				writeForbidden(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type parsedRequest struct {
	group     string
	version   string
	namespace string
	plural    string
	name      string
	named     bool
}

func parseRequestPath(rawPath string) (parsedRequest, error) {
	cleanPath := path.Clean(rawPath)
	if cleanPath != rawPath {
		return parsedRequest{}, fmt.Errorf("non-canonical path")
	}
	if strings.Contains(rawPath, "..") {
		return parsedRequest{}, fmt.Errorf("path traversal")
	}

	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	if len(parts) < 4 || parts[0] != "apis" {
		return parsedRequest{}, fmt.Errorf("not a public resource path")
	}
	// parts[1] is the API group and parts[2] is the version.
	i := 3
	result := parsedRequest{group: parts[1], version: parts[2]}
	if parts[i] == "namespaces" {
		if len(parts) <= i+2 || parts[i+1] == "" {
			return parsedRequest{}, fmt.Errorf("namespace and resource are required")
		}
		result.namespace = parts[i+1]
		result.plural = parts[i+2]
		i += 3
	} else {
		result.plural = parts[i]
		i++
	}

	// NodePools also have a nested parent route:
	// /namespaces/{namespace}/clusters/{clusterID}/nodepools.
	if result.plural == "clusters" && i < len(parts) {
		i++ // parent cluster name
		if i >= len(parts) || parts[i] != "nodepools" {
			if i == len(parts) {
				result.named = true
				result.name = parts[i-1]
				return result, nil
			}
			return parsedRequest{}, fmt.Errorf("invalid cluster child path")
		}
		result.plural = "nodepools"
		i++
	}

	if i < len(parts) {
		if i+1 != len(parts) || parts[i] == "" {
			return parsedRequest{}, fmt.Errorf("invalid resource path")
		}
		result.named = true
		result.name = parts[i]
		i++
	}
	if i != len(parts) {
		return parsedRequest{}, fmt.Errorf("invalid resource path")
	}
	return result, nil
}

func resourceForRequest(request parsedRequest, resources []types.ResourceInfo) (types.ResourceInfo, bool) {
	for _, resource := range resources {
		if resource.GVK.Group != request.group || resource.GVK.Version != request.version || resource.Plural != request.plural {
			continue
		}
		if resource.Namespaced != (request.namespace != "") {
			continue
		}
		return resource, true
	}
	return types.ResourceInfo{}, false
}

func isMetadataPath(requestPath string) bool {
	switch requestPath {
	case "/healthz", "/readyz", "/livez", "/apis", "/apis/":
		return true
	}
	if strings.HasPrefix(requestPath, "/openapi/") {
		return true
	}
	parts := strings.Split(strings.Trim(requestPath, "/"), "/")
	return len(parts) == 2 && parts[0] == "apis" || len(parts) == 3 && parts[0] == "apis"
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden"}`))
}
