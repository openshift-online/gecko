package placement

import (
	"encoding/json"
	"fmt"
	"os"
)

// DefaultCustomerLabelsPath is the path at which the customer-labels ConfigMap
// is mounted inside the gecko-placement container.
const DefaultCustomerLabelsPath = "/etc/config/customer-labels/customer-labels.json"

// LoadCustomerLabels reads a JSON file containing key-value label pairs and
// returns them as a map. Returns nil (no error) if the file does not exist.
func LoadCustomerLabels(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read customer labels file %s: %w", path, err)
	}
	var labels map[string]string
	if err := json.Unmarshal(data, &labels); err != nil {
		return nil, fmt.Errorf("parse customer labels file %s: %w", path, err)
	}
	return labels, nil
}
