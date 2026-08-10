// Package authz provides authorization configuration loading and validation
// for the platform RBAC system.
//
// It reads role definitions and bootstrap bindings from YAML files (typically
// mounted from a Kubernetes ConfigMap) and builds validation sets used by the
// Cedar policy engine.
package authz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// RoleDefinition represents a single role from the ConfigMap.
type RoleDefinition struct {
	Name        string   `yaml:"name"`
	Scope       string   `yaml:"scope"`       // "namespace" or "platform"
	Permissions []string `yaml:"permissions"` // e.g. "cluster.create", "nodepool.list"
}

// RolesConfig holds all role definitions from roles.yaml.
type RolesConfig struct {
	Roles []RoleDefinition `yaml:"roles"`
}

// BootstrapBinding represents an initial PlatformRoleBinding from bootstrap.yaml.
type BootstrapBinding struct {
	Name    string `yaml:"name"`
	Subject string `yaml:"subject"`
	RoleRef string `yaml:"roleRef"`
}

// BootstrapConfig holds bootstrap bindings from bootstrap.yaml.
type BootstrapConfig struct {
	PlatformRoleBindings []BootstrapBinding `yaml:"platformRoleBindings"`
}

// RoleConfig holds the complete parsed authorization configuration.
type RoleConfig struct {
	// Roles is the list of all defined roles.
	Roles []RoleDefinition
	// BootstrapBindings is the list of initial platform role bindings.
	BootstrapBindings []BootstrapBinding
	// RoleLabels is the set of all valid role names.
	RoleLabels map[string]bool
	// NamespaceRoleLabels is the set of namespace-scoped role names only.
	NamespaceRoleLabels map[string]bool
	// PlatformRoleLabels is the set of platform-scoped role names only.
	PlatformRoleLabels map[string]bool
}

// KnownPermissions is the complete set of valid permission strings.
var KnownPermissions = map[string]bool{
	"cluster.create": true, "cluster.list": true, "cluster.get": true,
	"cluster.update": true, "cluster.delete": true,
	"nodepool.create": true, "nodepool.list": true, "nodepool.get": true,
	"nodepool.update": true, "nodepool.delete": true,
	"rolebinding.create": true, "rolebinding.list": true, "rolebinding.get": true,
	"rolebinding.update": true, "rolebinding.delete": true,
	"platformrolebinding.create": true, "platformrolebinding.list": true,
	"platformrolebinding.get": true, "platformrolebinding.update": true,
	"platformrolebinding.delete": true,
	"customrole.create": true, "customrole.list": true, "customrole.get": true,
	"customrole.update": true, "customrole.delete": true,
}

// validScopes is the set of accepted role scope values.
var validScopes = map[string]bool{
	"namespace": true,
	"platform":  true,
}

// LoadConfig reads and validates authorization configuration from the given
// directory. It expects a roles.yaml file and an optional bootstrap.yaml file.
//
// The roles.yaml file must contain valid role definitions with known scopes
// and permissions. The bootstrap.yaml file, if present, must reference only
// platform-scoped roles.
func LoadConfig(configDir string) (*RoleConfig, error) {
	rolesData, err := os.ReadFile(filepath.Join(configDir, "roles.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading roles.yaml: %w", err)
	}

	var rolesConfig RolesConfig
	if err := yaml.Unmarshal(rolesData, &rolesConfig); err != nil {
		return nil, fmt.Errorf("parsing roles.yaml: %w", err)
	}

	rc := &RoleConfig{
		Roles:               rolesConfig.Roles,
		RoleLabels:          make(map[string]bool),
		NamespaceRoleLabels: make(map[string]bool),
		PlatformRoleLabels:  make(map[string]bool),
	}

	// Validate roles and build label sets.
	for i, role := range rc.Roles {
		if role.Name == "" {
			return nil, fmt.Errorf("role at index %d has empty name", i)
		}
		if !validScopes[role.Scope] {
			return nil, fmt.Errorf("role %q has invalid scope %q (must be \"namespace\" or \"platform\")", role.Name, role.Scope)
		}
		for _, perm := range role.Permissions {
			if !KnownPermissions[perm] {
				return nil, fmt.Errorf("role %q has unknown permission %q", role.Name, perm)
			}
		}

		rc.RoleLabels[role.Name] = true
		switch role.Scope {
		case "namespace":
			rc.NamespaceRoleLabels[role.Name] = true
		case "platform":
			rc.PlatformRoleLabels[role.Name] = true
		}
	}

	// Load bootstrap bindings (optional).
	bootstrapData, err := os.ReadFile(filepath.Join(configDir, "bootstrap.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rc, nil
		}
		return nil, fmt.Errorf("reading bootstrap.yaml: %w", err)
	}

	var bootstrapConfig BootstrapConfig
	if err := yaml.Unmarshal(bootstrapData, &bootstrapConfig); err != nil {
		return nil, fmt.Errorf("parsing bootstrap.yaml: %w", err)
	}

	// Validate bootstrap bindings: roleRef must reference a platform-scoped role.
	for i, binding := range bootstrapConfig.PlatformRoleBindings {
		if binding.RoleRef == "" {
			return nil, fmt.Errorf("bootstrap binding at index %d has empty roleRef", i)
		}
		if !rc.RoleLabels[binding.RoleRef] {
			return nil, fmt.Errorf("bootstrap binding %q references unknown role %q", binding.Name, binding.RoleRef)
		}
		if !rc.PlatformRoleLabels[binding.RoleRef] {
			return nil, fmt.Errorf("bootstrap binding %q references namespace-scoped role %q (must be platform-scoped)", binding.Name, binding.RoleRef)
		}
	}

	rc.BootstrapBindings = bootstrapConfig.PlatformRoleBindings
	return rc, nil
}

// resourceNames maps lowercase resource keys to their PascalCase names used
// in Cedar action identifiers.
var resourceNames = map[string]string{
	"cluster":             "Cluster",
	"nodepool":            "Nodepool",
	"rolebinding":         "RoleBinding",
	"platformrolebinding": "PlatformRoleBinding",
	"customrole":          "CustomRole",
}

// pluralResources lists resources whose list action uses a plural form
// (e.g. "ListNodepools" instead of "ListNodepool").
var pluralResources = map[string]string{
	"Cluster":             "Clusters",
	"Nodepool":            "Nodepools",
	"RoleBinding":         "RoleBindings",
	"PlatformRoleBinding": "PlatformRoleBindings",
	"CustomRole":          "CustomRoles",
}

// PermissionToAction converts a permission string (e.g. "cluster.create") to
// the corresponding Cedar action name (e.g. "CreateCluster").
//
// The format is PascalCase: capitalize the verb, then append the PascalCase
// resource name. For "list" actions, the resource is pluralized
// (e.g. "ListClusters").
func PermissionToAction(perm string) string {
	parts := strings.SplitN(perm, ".", 2)
	if len(parts) != 2 {
		return perm
	}

	resource, verb := parts[0], parts[1]

	// Look up the PascalCase resource name.
	resName, ok := resourceNames[resource]
	if !ok {
		// Fallback: capitalize first letter.
		resName = strings.ToUpper(resource[:1]) + resource[1:]
	}

	// Capitalize the verb.
	pascalVerb := strings.ToUpper(verb[:1]) + verb[1:]

	// Pluralize resource for "list" actions.
	if verb == "list" {
		if plural, ok := pluralResources[resName]; ok {
			resName = plural
		} else {
			resName = resName + "s"
		}
	}

	return pascalVerb + resName
}
