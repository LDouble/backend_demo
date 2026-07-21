// Package permissionmanifest exposes generated permission manifests to the runtime.
package permissionmanifest

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed modules/*.json
var manifests embed.FS

// Rule is one generated Casbin rule.
type Rule struct {
	Name        string   `json:"name"`
	PathPattern string   `json:"path_pattern"`
	Methods     []string `json:"methods"`
}

type manifest struct {
	Module      string `json:"module"`
	Permissions []Rule `json:"permissions"`
}

// Rules returns every generated permission exposed by the runtime API contract.
func Rules() ([]Rule, error) {
	entries, err := fs.ReadDir(manifests, "modules")
	if err != nil {
		return nil, fmt.Errorf("read embedded permission manifests: %w", err)
	}
	rules := []Rule{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := manifests.ReadFile("modules/" + entry.Name())
		if readErr != nil {
			return nil, fmt.Errorf("read permission manifest %q: %w", entry.Name(), readErr)
		}
		var value manifest
		if decodeErr := json.Unmarshal(data, &value); decodeErr != nil {
			return nil, fmt.Errorf("decode permission manifest %q: %w", entry.Name(), decodeErr)
		}
		rules = append(rules, value.Permissions...)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	return rules, nil
}

// MemberRules returns generated, non-admin notice permissions for the member role.
func MemberRules() ([]Rule, error) {
	all, err := Rules()
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(all))
	for _, rule := range all {
		if (strings.HasPrefix(rule.Name, "notice:") && strings.HasPrefix(rule.PathPattern, "/api/v1/notices")) || rule.Name == "core:getme" {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}
