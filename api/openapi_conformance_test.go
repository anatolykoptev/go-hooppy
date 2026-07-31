// Package api contains the measured OpenAPI conformance test.
//
// Every file in testdata/live/ validates against its schema in
// api/openapi-measured.yaml. A fixture that does not match fails the build.
// The fixture list is NOT hand-maintained — the test walks the directory, so a
// newly recorded fixture is covered automatically (and a fixture with no
// schema in the spec fails, proving the walk is real).
//
// The schemas are DERIVED from the fixtures by cmd/specgen. By construction
// they match at generation time. The test catches the case where either side
// drifts independently: a fixture corrupted after generation (F1) or a new
// fixture added without re-running the generator (F2).
package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

// specFile is the measured OpenAPI document, relative to this test file.
const specFile = "openapi-measured.yaml"

// fixtureDir is the directory of recorded live responses, relative to this test file.
const fixtureDir = "../testdata/live"

// TestOpenAPIConformance walks testdata/live/, loads the measured spec, and
// validates every fixture against its schema. A fixture with no schema in the
// spec fails (F2). A fixture that does not match its schema fails (F1).
func TestOpenAPIConformance(t *testing.T) {
	// 1. Load the spec.
	specData, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("read spec %s: %v", specFile, err)
	}
	var spec map[string]interface{}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse spec %s: %v", specFile, err)
	}

	// 2. Extract components.schemas.
	components, _ := spec["components"].(map[string]interface{})
	schemasMap, _ := components["schemas"].(map[string]interface{})
	if len(schemasMap) == 0 {
		t.Fatal("no schemas in spec")
	}

	// 3. Build fixture→schema-name mapping from x-fixture entries in paths.
	fixtureToSchema := buildFixtureMapping(t, spec)

	// 4. Walk the fixture directory.
	fixtures, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	sort.Strings(fixtures)

	if len(fixtures) == 0 {
		t.Fatalf("no fixtures found in %s", fixtureDir)
	}

	// 5. Validate each fixture against its schema.
	for _, fpath := range fixtures {
		fname := filepath.Base(fpath)
		fixtureKey := "testdata/live/" + fname

		t.Run(fname, func(t *testing.T) {
			schemaName, ok := fixtureToSchema[fixtureKey]
			if !ok {
				t.Fatalf("fixture %s has no schema in the spec — run `GOWORK=off go run ./cmd/specgen` to regenerate", fixtureKey)
			}

			schemaRaw, ok := schemasMap[schemaName].(map[string]interface{})
			if !ok {
				t.Fatalf("schema %s not found in components.schemas", schemaName)
			}

			// Convert the schema map to JSON for jsonschema-go.
			schemaJSON, err := json.Marshal(schemaRaw)
			if err != nil {
				t.Fatalf("marshal schema %s: %v", schemaName, err)
			}

			var schema jsonschema.Schema
			if err := json.Unmarshal(schemaJSON, &schema); err != nil {
				t.Fatalf("unmarshal schema %s: %v", schemaName, err)
			}

			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatalf("resolve schema %s: %v", schemaName, err)
			}

			// Load and parse the fixture.
			fixtureData, err := os.ReadFile(fpath)
			if err != nil {
				t.Fatalf("read fixture %s: %v", fpath, err)
			}
			var instance interface{}
			if err := json.Unmarshal(fixtureData, &instance); err != nil {
				t.Fatalf("parse fixture %s: %v", fname, err)
			}

			if err := resolved.Validate(instance); err != nil {
				t.Errorf("fixture %s does not validate against schema %s:\n%v", fname, schemaName, err)
			}
		})
	}
}

// buildFixtureMapping walks the spec's paths and collects every x-fixture entry,
// returning a map of fixture path → schema name.
func buildFixtureMapping(t *testing.T, spec map[string]interface{}) map[string]string {
	paths, _ := spec["paths"].(map[string]interface{})
	if len(paths) == 0 {
		t.Fatal("no paths in spec")
	}

	mapping := make(map[string]string)
	for _, pathVal := range paths {
		pathMap, ok := pathVal.(map[string]interface{})
		if !ok {
			continue
		}
		for _, methodVal := range pathMap {
			opMap, ok := methodVal.(map[string]interface{})
			if !ok {
				continue
			}
			responses, _ := opMap["responses"].(map[string]interface{})
			for _, respVal := range responses {
				respMap, ok := respVal.(map[string]interface{})
				if !ok {
					continue
				}
				xFixture, ok := respMap["x-fixture"].(map[string]interface{})
				if !ok {
					continue
				}
				for fixturePath, schemaRef := range xFixture {
					refStr, ok := schemaRef.(string)
					if !ok {
						continue
					}
					// Extract schema name from $ref: #/components/schemas/<Name>
					schemaName := strings.TrimPrefix(refStr, "#/components/schemas/")
					mapping[fixturePath] = schemaName
				}
			}
		}
	}
	return mapping
}

// TestSpecHasProvenanceOnEveryOperation verifies that every operation in the
// spec carries an x-provenance extension. A spec that mixes measurement and
// inference without saying which is worse than no spec.
func TestSpecHasProvenanceOnEveryOperation(t *testing.T) {
	specData, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec map[string]interface{}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	paths, _ := spec["paths"].(map[string]interface{})
	var missing []string
	for pathStr, pathVal := range paths {
		pathMap, ok := pathVal.(map[string]interface{})
		if !ok {
			continue
		}
		for method, methodVal := range pathMap {
			if method == "x-provenance" || method == "x-vendor-spec" {
				continue
			}
			opMap, ok := methodVal.(map[string]interface{})
			if !ok {
				continue
			}
			if _, ok := opMap["x-provenance"]; !ok {
				missing = append(missing, fmt.Sprintf("%s %s", method, pathStr))
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("operations missing x-provenance:\n%s", strings.Join(missing, "\n"))
	}
}
