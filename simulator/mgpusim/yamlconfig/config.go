package yamlconfig

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

var OverrideConfig map[string]string

// LoadYAMLFile reads the YAML file at the given path and returns a flattened
// map[string]string. Nested maps are joined with '.' and arrays use '[index]'.
// Returns an error if the file cannot be read or the YAML cannot be parsed.
func LoadYAMLFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var data interface{}
	if err := yaml.Unmarshal(b, &data); err != nil {
		return err
	}

	out := make(map[string]string)
	flatten("", data, out)

	OverrideConfig = out

	return checkViolations()
}

// flatten recursively flattens an arbitrary YAML-parsed structure into out.
// If prefix is empty the current key is used directly, otherwise keys are
// joined as prefix + "." + key. Array elements use prefix + "[i]" notation.
func flatten(prefix string, v interface{}, out map[string]string) {
	switch vv := v.(type) {
	case map[string]interface{}:
		for k, val := range vv {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, val, out)
		}
	case []interface{}:
		for i, val := range vv {
			key := fmt.Sprintf("%s[%d]", prefix, i)
			flatten(key, val, out)
		}
	case nil:
		out[prefix] = ""
	default:
		// Convert scalar values to string for storage
		out[prefix] = fmt.Sprint(vv)
	}
}

// checkViolations checks if there are any violations in the configuration.
// It returns an error if any violations are found.
func checkViolations() error {
	// MMU configuration violation
	if mmuType, ok := OverrideConfig["MMU.type"]; ok {
		switch mmuType {
		case "IdealMMU":
			if _, ok := OverrideConfig["MMU.walkLatency"]; !ok {
				return fmt.Errorf("IdealMMU requires walkLatency to be set")
			}
		case "MPWMMU", "BaselineMMU", "CaPWQMMUL1", "CaPWQMMUL2", "CaPWQMMUL3", "CaPWQMMUL4", "CaPWQMMUL5":
			if _, ok := OverrideConfig["MMU.numPageWalkers"]; !ok {
				return fmt.Errorf("%s requires numPageWalkers to be set", mmuType)
			}
		}
	}

	for key, val := range OverrideConfig {
		log.Printf("overriding %s with value %s", key, val)
	}

	return nil
}
