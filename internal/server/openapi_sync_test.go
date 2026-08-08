package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var documentedRoutePattern = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ((?:/api|/v1)[^"]+)"`)

func TestOpenAPISpecMatchesDocumentedRoutes(t *testing.T) {
	t.Helper()

	declared := readDeclaredDocumentedRoutes(t)
	documented := readDocumentedOperations(t)

	missingDocs := diffOperationSets(declared, documented)
	staleDocs := diffOperationSets(documented, declared)
	if len(missingDocs) == 0 && len(staleDocs) == 0 {
		return
	}

	var parts []string
	if len(missingDocs) > 0 {
		parts = append(parts, "routes missing from openapi/local-api.json:\n  - "+strings.Join(missingDocs, "\n  - "))
	}
	if len(staleDocs) > 0 {
		parts = append(parts, "documented operations missing from internal/server/routes.go:\n  - "+strings.Join(staleDocs, "\n  - "))
	}

	t.Fatalf(
		"OpenAPI spec is out of sync.\n\n%s\n\nUpdate openapi/local-api.json whenever /api/* or /v1/* routes change.",
		strings.Join(parts, "\n\n"),
	)
}

func TestProviderScopedRouteFamilyIsDocumented(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "local-api.json")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}
	if _, ok := spec.Paths["/providers/{providerID}/v1/{endpoint}"]; ok {
		t.Fatal("provider-scoped OpenAPI paths must not use a slash-containing endpoint parameter")
	}
	required := map[string][]string{
		"/providers/{providerID}/v1/models":                {"get"},
		"/providers/{providerID}/v1/chat/completions":      {"post"},
		"/providers/{providerID}/v1/responses":             {"get", "post"},
		"/providers/{providerID}/v1/messages":              {"post"},
		"/providers/{providerID}/v1/messages/count_tokens": {"post"},
		"/providers/{providerID}/v1/embeddings":            {"post"},
		"/providers/{providerID}/v1/images/generations":    {"post"},
		"/providers/{providerID}/v1/images/edits":          {"post"},
		"/providers/{providerID}/v1/audio/transcriptions":  {"post"},
	}
	for path, methods := range required {
		item, ok := spec.Paths[path]
		if !ok {
			t.Errorf("provider-scoped path %s is missing from OpenAPI", path)
			continue
		}
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("provider-scoped operation %s %s is missing from OpenAPI", strings.ToUpper(method), path)
			}
		}
	}
}

func readDeclaredDocumentedRoutes(t *testing.T) map[string]struct{} {
	t.Helper()

	data, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	operations := make(map[string]struct{})
	matches := documentedRoutePattern.FindAllStringSubmatch(string(data), -1)
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		operations[match[1]+" "+match[2]] = struct{}{}
	}
	if len(operations) == 0 {
		t.Fatal("no documented /api or /v1 routes found in routes.go")
	}

	return operations
}

func readDocumentedOperations(t *testing.T) map[string]struct{} {
	t.Helper()

	specPath := filepath.Join("..", "..", "openapi", "local-api.json")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}

	allowedMethods := map[string]struct{}{
		"get":     {},
		"post":    {},
		"put":     {},
		"patch":   {},
		"delete":  {},
		"head":    {},
		"options": {},
	}

	operations := make(map[string]struct{})
	for path, item := range spec.Paths {
		if !strings.HasPrefix(path, "/api") && !strings.HasPrefix(path, "/v1") {
			continue
		}
		for method := range item {
			if _, ok := allowedMethods[strings.ToLower(method)]; !ok {
				continue
			}
			operations[strings.ToUpper(method)+" "+path] = struct{}{}
		}
	}
	if len(operations) == 0 {
		t.Fatalf("no documented /api or /v1 operations found in %s", specPath)
	}

	return operations
}

func diffOperationSets(left, right map[string]struct{}) []string {
	var diff []string
	for op := range left {
		if _, ok := right[op]; ok {
			continue
		}
		diff = append(diff, op)
	}
	sort.Strings(diff)
	return diff
}
