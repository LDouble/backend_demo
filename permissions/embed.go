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

// CatalogEntry is one stable permission exposed to administrative clients.
type CatalogEntry struct {
	Code        string   `json:"code"`
	Module      string   `json:"module"`
	PathPattern string   `json:"path_pattern"`
	Methods     []string `json:"methods"`
}

type manifest struct {
	Module      string `json:"module"`
	Permissions []Rule `json:"permissions"`
}

// Rules returns every generated permission exposed by the runtime API contract.
func Rules() ([]Rule, error) {
	catalog, err := Catalog()
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(catalog))
	for _, entry := range catalog {
		rules = append(rules, Rule{
			Name:        entry.Code,
			PathPattern: entry.PathPattern,
			Methods:     append([]string{}, entry.Methods...),
		})
	}
	return rules, nil
}

// Catalog returns the generated permission directory in deterministic order.
func Catalog() ([]CatalogEntry, error) {
	entries, err := fs.ReadDir(manifests, "modules")
	if err != nil {
		return nil, fmt.Errorf("read embedded permission manifests: %w", err)
	}
	catalog := []CatalogEntry{}
	seen := map[string]struct{}{}
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
		for _, rule := range value.Permissions {
			methods := normalizedMethods(rule.Methods)
			key := rule.Name + "\x00" + value.Module + "\x00" + rule.PathPattern + "\x00" + strings.Join(methods, "\x00")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			catalog = append(catalog, CatalogEntry{
				Code:        rule.Name,
				Module:      value.Module,
				PathPattern: rule.PathPattern,
				Methods:     methods,
			})
		}
	}
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Module != catalog[j].Module {
			return catalog[i].Module < catalog[j].Module
		}
		if catalog[i].Code != catalog[j].Code {
			return catalog[i].Code < catalog[j].Code
		}
		return catalog[i].PathPattern < catalog[j].PathPattern
	})
	return catalog, nil
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

func normalizedMethods(methods []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(methods))
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			continue
		}
		if _, exists := seen[method]; exists {
			continue
		}
		seen[method] = struct{}{}
		result = append(result, method)
	}
	sort.Strings(result)
	return result
}
