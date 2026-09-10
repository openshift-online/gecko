package v1

import (
	"os"
	"regexp"
	"testing"
)

func TestReleaseVersionPattern(t *testing.T) {
	source, err := os.ReadFile("cluster_types.go")
	if err != nil {
		t.Fatalf("read cluster types: %v", err)
	}
	marker := regexp.MustCompile(`\+kubebuilder:validation:Pattern=\x60([^\x60]+)\x60`).FindSubmatch(source)
	if len(marker) != 2 {
		t.Fatal("release version validation pattern not found")
	}
	pattern, err := regexp.Compile(string(marker[1]))
	if err != nil {
		t.Fatalf("compile release version pattern: %v", err)
	}

	tests := []struct {
		version string
		valid   bool
	}{
		{version: "4.24.1", valid: true},
		{version: "4.24.1-rc.1", valid: true},
		{version: "4.24.1-01", valid: false},
		{version: "4.24.1-alpha.01", valid: false},
	}
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if actual := pattern.MatchString(test.version); actual != test.valid {
				t.Fatalf("pattern match = %t, want %t", actual, test.valid)
			}
		})
	}
}
