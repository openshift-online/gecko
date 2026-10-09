package v1

import "testing"

func TestControlPlaneUpgradePolicyPermissionsAreValid(t *testing.T) {
	permissions := []string{
		"controlplaneupgradepolicy.create",
		"controlplaneupgradepolicy.list",
		"controlplaneupgradepolicy.get",
		"controlplaneupgradepolicy.update",
		"controlplaneupgradepolicy.delete",
	}

	for _, permission := range permissions {
		if !IsValidAuthorizationPermission(permission) {
			t.Errorf("permission %q is not valid", permission)
		}
	}
}

func TestControlPlaneUpgradeRequestPermissionsAreValid(t *testing.T) {
	permissions := []string{
		"controlplaneupgraderequest.create",
		"controlplaneupgraderequest.list",
		"controlplaneupgraderequest.get",
	}

	for _, permission := range permissions {
		if !IsValidAuthorizationPermission(permission) {
			t.Errorf("permission %q is not valid", permission)
		}
	}
}
