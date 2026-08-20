package authz

// PermissionToAction maps permission strings to Cedar action names.
var PermissionToAction = map[string]string{
	"cluster.create":    "CreateCluster",
	"cluster.list":      "ListClusters",
	"cluster.get":       "GetCluster",
	"cluster.update":    "UpdateCluster",
	"cluster.delete":    "DeleteCluster",
	"nodepool.create":   "CreateNodepool",
	"nodepool.list":     "ListNodepools",
	"nodepool.get":      "GetNodepool",
	"nodepool.update":   "UpdateNodepool",
	"nodepool.delete":   "DeleteNodepool",
	"rolebinding.create": "CreateRoleBinding",
	"rolebinding.list":   "ListRoleBindings",
	"rolebinding.get":    "GetRoleBinding",
	"rolebinding.update": "UpdateRoleBinding",
	"rolebinding.delete": "DeleteRoleBinding",
	"role.create":       "CreateRole",
	"role.list":         "ListRoles",
	"role.get":          "GetRole",
	"role.update":       "UpdateRole",
	"role.delete":       "DeleteRole",
}

// ValidPermissions is the set of all valid permission strings.
var ValidPermissions = func() map[string]bool {
	m := make(map[string]bool, len(PermissionToAction))
	for k := range PermissionToAction {
		m[k] = true
	}
	return m
}()

// InfraWritePermissions are permissions that modify infrastructure.
var InfraWritePermissions = map[string]bool{
	"cluster.create":  true,
	"cluster.update":  true,
	"cluster.delete":  true,
	"nodepool.create": true,
	"nodepool.update": true,
	"nodepool.delete": true,
}

// InfraReadPermissions are permissions that read infrastructure.
var InfraReadPermissions = map[string]bool{
	"cluster.list":  true,
	"cluster.get":   true,
	"nodepool.list": true,
	"nodepool.get":  true,
}

// ActionToPermission is the reverse mapping from Cedar action to permission.
var ActionToPermission = func() map[string]string {
	m := make(map[string]string, len(PermissionToAction))
	for perm, action := range PermissionToAction {
		m[action] = perm
	}
	return m
}()

// ResourcePluralToActions maps plural resource names and HTTP methods to Cedar action names.
var ResourcePluralToActions = map[string]map[string]string{
	"clusters": {
		"POST":   "CreateCluster",
		"GET":    "ListClusters",
		"PUT":    "UpdateCluster",
		"PATCH":  "UpdateCluster",
		"DELETE": "DeleteCluster",
	},
	"nodepools": {
		"POST":   "CreateNodepool",
		"GET":    "ListNodepools",
		"PUT":    "UpdateNodepool",
		"PATCH":  "UpdateNodepool",
		"DELETE": "DeleteNodepool",
	},
	"rolebindings": {
		"POST":   "CreateRoleBinding",
		"GET":    "ListRoleBindings",
		"PUT":    "UpdateRoleBinding",
		"PATCH":  "UpdateRoleBinding",
		"DELETE": "DeleteRoleBinding",
	},
	"roles": {
		"POST":   "CreateRole",
		"GET":    "ListRoles",
		"PUT":    "UpdateRole",
		"PATCH":  "UpdateRole",
		"DELETE": "DeleteRole",
	},
}

// ResourceSingularGetAction maps plural resource names to their Get action (used when {name} is present).
var ResourceSingularGetAction = map[string]string{
	"clusters":     "GetCluster",
	"nodepools":    "GetNodepool",
	"rolebindings": "GetRoleBinding",
	"roles":        "GetRole",
}
