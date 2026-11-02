package authz

import (
	"fmt"
	"net/http"
)

// Action is the Cedar action identifier used for a public API operation.
type Action string

const (
	CreateCluster     Action = "CreateCluster"
	ListClusters      Action = "ListClusters"
	GetCluster        Action = "GetCluster"
	UpdateCluster     Action = "UpdateCluster"
	DeleteCluster     Action = "DeleteCluster"
	CreateNodepool    Action = "CreateNodepool"
	ListNodepools     Action = "ListNodepools"
	GetNodepool       Action = "GetNodepool"
	UpdateNodepool    Action = "UpdateNodepool"
	DeleteNodepool    Action = "DeleteNodepool"
	CreateRoleBinding Action = "CreateRoleBinding"
	ListRoleBindings  Action = "ListRoleBindings"
	GetRoleBinding    Action = "GetRoleBinding"
	UpdateRoleBinding Action = "UpdateRoleBinding"
	DeleteRoleBinding Action = "DeleteRoleBinding"
	CreateRole        Action = "CreateRole"
	ListRoles         Action = "ListRoles"
	GetRole           Action = "GetRole"
	UpdateRole        Action = "UpdateRole"
	DeleteRole        Action = "DeleteRole"
	GetVersion        Action = "GetVersion"
	ListVersions      Action = "ListVersions"
	GetChannel        Action = "GetChannel"
	ListChannels      Action = "ListChannels"
)

var permissionActions = map[string]Action{
	"cluster.create":     CreateCluster,
	"cluster.list":       ListClusters,
	"cluster.get":        GetCluster,
	"cluster.update":     UpdateCluster,
	"cluster.delete":     DeleteCluster,
	"nodepool.create":    CreateNodepool,
	"nodepool.list":      ListNodepools,
	"nodepool.get":       GetNodepool,
	"nodepool.update":    UpdateNodepool,
	"nodepool.delete":    DeleteNodepool,
	"rolebinding.create": CreateRoleBinding,
	"rolebinding.list":   ListRoleBindings,
	"rolebinding.get":    GetRoleBinding,
	"rolebinding.update": UpdateRoleBinding,
	"rolebinding.delete": DeleteRoleBinding,
	"role.create":        CreateRole,
	"role.list":          ListRoles,
	"role.get":           GetRole,
	"role.update":        UpdateRole,
	"role.delete":        DeleteRole,
	"version.get":        GetVersion,
	"version.list":       ListVersions,
	"channel.get":        GetChannel,
	"channel.list":       ListChannels,
}

func isPlatformAction(action Action) bool {
	switch action {
	case GetVersion, ListVersions, GetChannel, ListChannels:
		return true
	default:
		return false
	}
}

func actionForPermission(permission string) (Action, bool) {
	action, ok := permissionActions[permission]
	return action, ok
}

func actionForRequest(method, plural string, named bool) (Action, error) {
	verb, err := verbForRequest(method, named)
	if err != nil {
		return "", err
	}
	if verb == "patch" {
		verb = "update"
	}
	action, ok := actionForPermission(pluralToResource(plural) + "." + verb)
	if !ok {
		return "", fmt.Errorf("unsupported public resource %q", plural)
	}
	return action, nil
}

func verbForRequest(method string, named bool) (string, error) {
	switch method {
	case http.MethodGet:
		if named {
			return "get", nil
		}
		return "list", nil
	case http.MethodPost:
		if named {
			return "", fmt.Errorf("POST is not valid for a named resource")
		}
		return "create", nil
	case http.MethodPut, http.MethodPatch:
		if !named {
			return "", fmt.Errorf("%s is not valid for a collection", method)
		}
		if method == http.MethodPatch {
			return "patch", nil
		}
		return "update", nil
	case http.MethodDelete:
		if !named {
			return "", fmt.Errorf("DELETE is not valid for a collection")
		}
		return "delete", nil
	default:
		return "", fmt.Errorf("unsupported HTTP method %q", method)
	}

}

func pluralToResource(plural string) string {
	switch plural {
	case "clusters":
		return "cluster"
	case "nodepools":
		return "nodepool"
	case "roles":
		return "role"
	case "rolebindings":
		return "rolebinding"
	case "versions":
		return "version"
	case "channels":
		return "channel"
	default:
		return plural
	}
}
