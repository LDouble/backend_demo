package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// GenerateOpenAPIOptions controls global contract composition.
type GenerateOpenAPIOptions struct {
	Root  string
	Check bool
}

// GenerateOpenAPI composes generated module operations into the public contract.
// Existing generated operations are removed before composition, making repeated runs deterministic.
func GenerateOpenAPI(ctx context.Context, options GenerateOpenAPIOptions) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return false, fmt.Errorf("resolve repository root: %w", err)
	}
	contractPath, err := safeJoin(root, "api/openapi.yaml")
	if err != nil {
		return false, err
	}
	// #nosec G304 -- safeJoin confines the generated contract path to the repository root.
	current, err := repositoryReadFile(root, contractPath)
	if err != nil {
		return false, fmt.Errorf("read public OpenAPI contract: %w", err)
	}
	contract, err := decodeYAMLDocument(current)
	if err != nil {
		return false, fmt.Errorf("decode public OpenAPI contract: %w", err)
	}
	paths, err := mappingValue(contract, "paths")
	if err != nil {
		return false, err
	}
	removeGeneratedOperations(paths)

	modules, err := ListModules(ctx, root)
	if err != nil {
		return false, err
	}
	for _, module := range modules {
		fragmentPath, joinErr := safeJoin(root, "api/modules/"+module.Name+".yaml")
		if joinErr != nil {
			return false, joinErr
		}
		// #nosec G304 -- safeJoin confines generated module fragments to the repository root.
		fragmentData, readErr := repositoryReadFile(root, fragmentPath)
		if errors.Is(readErr, fs.ErrNotExist) {
			return false, fmt.Errorf("module OpenAPI fragment missing: %s", fragmentPath)
		}
		if readErr != nil {
			return false, fmt.Errorf("read module OpenAPI fragment: %w", readErr)
		}
		fragment, decodeErr := decodeYAMLDocument(fragmentData)
		if decodeErr != nil {
			return false, fmt.Errorf("decode module %q OpenAPI: %w", module.Name, decodeErr)
		}
		fragmentPaths, mapErr := mappingValue(fragment, "paths")
		if mapErr != nil {
			return false, mapErr
		}
		if mergeErr := mergeGeneratedPaths(paths, fragmentPaths, module.Name); mergeErr != nil {
			return false, mergeErr
		}
	}
	if err := validateOperationIDs(paths); err != nil {
		return false, err
	}
	content, err := encodeYAMLDocument(contract)
	if err != nil {
		return false, err
	}
	permissionPath, err := safeJoin(root, "permissions/modules/core.json")
	if err != nil {
		return false, err
	}
	permissionContent, err := corePermissionManifest(paths)
	if err != nil {
		return false, err
	}
	currentPermissions, permissionReadErr := repositoryReadFile(root, permissionPath)
	if permissionReadErr != nil && !errors.Is(permissionReadErr, fs.ErrNotExist) {
		return false, fmt.Errorf("read core permission manifest: %w", permissionReadErr)
	}
	contractChanged := !bytes.Equal(current, content)
	permissionsChanged := permissionReadErr != nil || !bytes.Equal(currentPermissions, permissionContent)
	changed := contractChanged || permissionsChanged
	if options.Check {
		if contractChanged {
			return true, fmt.Errorf("%w: api/openapi.yaml", ErrDrift)
		}
		if permissionsChanged {
			return true, fmt.Errorf("%w: permissions/modules/core.json", ErrDrift)
		}
		return false, nil
	}
	if contractChanged {
		if err := atomicWrite(root, contractPath, content); err != nil {
			return false, err
		}
	}
	if permissionsChanged {
		if err := atomicWrite(root, permissionPath, permissionContent); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func corePermissionManifest(paths *yaml.Node) ([]byte, error) {
	permissions := []Permission{}
	httpMethods := map[string]struct{}{"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}}
	for i := 0; i < len(paths.Content); i += 2 {
		path := paths.Content[i].Value
		if !strings.HasPrefix(path, "/api/v1/") {
			continue
		}
		item := paths.Content[i+1]
		for j := 0; j < len(item.Content); j += 2 {
			method := strings.ToLower(item.Content[j].Value)
			if _, ok := httpMethods[method]; !ok {
				continue
			}
			operation := item.Content[j+1]
			if mappingHas(operation, "x-generated-module") {
				continue
			}
			operationID := scalarMappingValue(operation, "operationId")
			if operationID == "" || (strings.HasPrefix(path, "/api/v1/auth/") && operationID != "GetMe") {
				continue
			}
			pattern := path
			for strings.Contains(pattern, "{") {
				start := strings.IndexByte(pattern, '{')
				end := strings.IndexByte(pattern[start:], '}')
				if end < 0 {
					return nil, fmt.Errorf("invalid core permission path %q", path)
				}
				end += start
				pattern = pattern[:start] + ":" + pattern[start+1:end] + pattern[end+1:]
			}
			permissions = append(permissions, Permission{Name: "core:" + strings.ToLower(operationID), Path: pattern, Methods: []string{strings.ToUpper(method)}})
		}
	}
	sort.Slice(permissions, func(i, j int) bool { return permissions[i].Name < permissions[j].Name })
	data, err := json.MarshalIndent(struct {
		Module      string       `json:"module"`
		Permissions []Permission `json:"permissions"`
	}{Module: "core", Permissions: permissions}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode core permission manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func decodeYAMLDocument(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("OpenAPI document must be a mapping")
	}
	return &document, nil
}

func encodeYAMLDocument(document *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode public OpenAPI contract: %w", err)
	}
	return buffer.Bytes(), nil
}

func mappingValue(document *yaml.Node, key string) (*yaml.Node, error) {
	mapping := document.Content[0]
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			value := mapping.Content[i+1]
			if value.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("OpenAPI %s must be a mapping", key)
			}
			return value, nil
		}
	}
	return nil, fmt.Errorf("OpenAPI document has no %s", key)
}

func removeGeneratedOperations(paths *yaml.Node) {
	for i := len(paths.Content) - 2; i >= 0; i -= 2 {
		pathItem := paths.Content[i+1]
		for j := len(pathItem.Content) - 2; j >= 0; j -= 2 {
			operation := pathItem.Content[j+1]
			isCurrentGenerated := mappingHas(operation, "x-generated-module")
			if isCurrentGenerated {
				pathItem.Content = append(pathItem.Content[:j], pathItem.Content[j+2:]...)
			}
		}
		if len(pathItem.Content) == 0 {
			paths.Content = append(paths.Content[:i], paths.Content[i+2:]...)
		}
	}
}

func mergeGeneratedPaths(target, source *yaml.Node, module string) error {
	for i := 0; i < len(source.Content); i += 2 {
		path := source.Content[i].Value
		sourceItem := source.Content[i+1]
		targetItem := mappingEntry(target, path)
		if targetItem == nil {
			targetItem = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		added := false
		for j := 0; j < len(sourceItem.Content); j += 2 {
			method := sourceItem.Content[j].Value
			operation := sourceItem.Content[j+1]
			if !mappingHas(operation, "x-generated-module") {
				continue
			}
			if mappingEntry(targetItem, method) != nil {
				return fmt.Errorf("module %q conflicts with existing OpenAPI operation %s %s", module, method, path)
			}
			targetItem.Content = append(targetItem.Content, cloneNode(sourceItem.Content[j]), cloneNode(operation))
			added = true
		}
		if mappingEntry(target, path) == nil && added {
			target.Content = append(target.Content, cloneNode(source.Content[i]), targetItem)
		}
	}
	return nil
}

func validateOperationIDs(paths *yaml.Node) error {
	seen := map[string]string{}
	for i := 0; i < len(paths.Content); i += 2 {
		path := paths.Content[i].Value
		item := paths.Content[i+1]
		for j := 0; j < len(item.Content); j += 2 {
			method := item.Content[j].Value
			operationID := scalarMappingValue(item.Content[j+1], "operationId")
			if operationID == "" {
				continue
			}
			location := method + " " + path
			if previous, ok := seen[operationID]; ok {
				return fmt.Errorf("duplicate OpenAPI operationId %q at %s and %s", operationID, previous, location)
			}
			seen[operationID] = location
		}
	}
	return nil
}

func mappingHas(node *yaml.Node, key string) bool { return mappingEntry(node, key) != nil }

func mappingEntry(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func scalarMappingValue(node *yaml.Node, key string) string {
	value := mappingEntry(node, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

func cloneNode(node *yaml.Node) *yaml.Node {
	copyValue := *node
	copyValue.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copyValue.Content[i] = cloneNode(child)
	}
	return &copyValue
}
