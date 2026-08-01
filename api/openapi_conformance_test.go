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
	"regexp"
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

// rewriteRefs rebases every "#/components/schemas/X" pointer onto "#/$defs/X"
// so a response schema can be resolved as a self-contained document.
func rewriteRefs(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					out[k] = strings.Replace(s, "#/components/schemas/", "#/$defs/", 1)
					continue
				}
			}
			out[k] = rewriteRefs(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = rewriteRefs(val)
		}
		return out
	default:
		return v
	}
}

// TestResponseSchemasValidateTheirFixtures validates every recorded fixture
// against the schema published on the RESPONSE, not only against its named
// component.
//
// TestOpenAPIConformance above reads components.schemas[name] and never looks
// at responses.*.content.*.schema, so a defect in how the generator composes
// that schema is invisible to it. That is not hypothetical: an endpoint with
// two recorded shapes was emitted as a oneOf, and because a derived empty form
// is a SUBSET of the populated one (an empty array vacuously satisfies any
// items constraint), both fixtures matched both branches and oneOf — "exactly
// one" — rejected the very bodies the schema was generated from. The whole
// suite stayed green. This test reds on that.
func TestResponseSchemasValidateTheirFixtures(t *testing.T) {
	specData, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("read spec %s: %v", specFile, err)
	}
	var spec map[string]interface{}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse spec %s: %v", specFile, err)
	}

	components, _ := spec["components"].(map[string]interface{})
	schemasMap, _ := components["schemas"].(map[string]interface{})
	if len(schemasMap) == 0 {
		t.Fatal("no schemas in spec")
	}
	defs := rewriteRefs(schemasMap)

	paths, _ := spec["paths"].(map[string]interface{})
	if len(paths) == 0 {
		t.Fatal("no paths in spec")
	}

	checked := 0
	pathNames := make([]string, 0, len(paths))
	for p := range paths {
		pathNames = append(pathNames, p)
	}
	sort.Strings(pathNames)

	for _, pathName := range pathNames {
		pathMap, ok := paths[pathName].(map[string]interface{})
		if !ok {
			continue
		}
		for method, opVal := range pathMap {
			opMap, ok := opVal.(map[string]interface{})
			if !ok {
				continue
			}
			responses, ok := opMap["responses"].(map[string]interface{})
			if !ok {
				continue
			}
			for status, respVal := range responses {
				respMap, ok := respVal.(map[string]interface{})
				if !ok {
					continue
				}
				fixtures, ok := respMap["x-fixture"].(map[string]interface{})
				if !ok || len(fixtures) == 0 {
					continue
				}
				content, ok := respMap["content"].(map[string]interface{})
				if !ok {
					t.Errorf("%s %s %s: has x-fixture but no content schema", method, pathName, status)
					continue
				}
				mediaType, ok := content["application/json"].(map[string]interface{})
				if !ok {
					continue
				}
				rawSchema, ok := mediaType["schema"].(map[string]interface{})
				if !ok {
					continue
				}

				// Self-contained document: the response schema plus every
				// component schema as $defs.
				doc, _ := rewriteRefs(rawSchema).(map[string]interface{})
				selfContained := make(map[string]interface{}, len(doc)+1)
				for k, v := range doc {
					selfContained[k] = v
				}
				selfContained["$defs"] = defs

				schemaJSON, err := json.Marshal(selfContained)
				if err != nil {
					t.Fatalf("marshal response schema for %s %s: %v", method, pathName, err)
				}
				var schema jsonschema.Schema
				if err := json.Unmarshal(schemaJSON, &schema); err != nil {
					t.Fatalf("unmarshal response schema for %s %s: %v", method, pathName, err)
				}
				resolved, err := schema.Resolve(nil)
				if err != nil {
					t.Fatalf("resolve response schema for %s %s: %v", method, pathName, err)
				}

				fixtureKeys := make([]string, 0, len(fixtures))
				for k := range fixtures {
					fixtureKeys = append(fixtureKeys, k)
				}
				sort.Strings(fixtureKeys)

				for _, key := range fixtureKeys {
					fname := filepath.Base(key)
					t.Run(fmt.Sprintf("%s_%s", strings.TrimPrefix(pathName, "/"), fname), func(t *testing.T) {
						data, err := os.ReadFile(filepath.Join(fixtureDir, fname))
						if err != nil {
							t.Fatalf("read fixture %s: %v", fname, err)
						}
						var instance interface{}
						if err := json.Unmarshal(data, &instance); err != nil {
							t.Fatalf("parse fixture %s: %v", fname, err)
						}
						if err := resolved.Validate(instance); err != nil {
							t.Errorf("fixture %s does not validate against the RESPONSE schema of %s %s (%s):\n%v", fname, strings.ToUpper(method), pathName, status, err)
						}
					})
					checked++
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no response schemas carried x-fixture entries — the walk found nothing, so a green result here proves nothing")
	}
	t.Logf("validated %d fixture/response-schema pairs", checked)
}

// TestNoDuplicatePathTemplates fails when two paths are the same template with
// differently-named parameters. OpenAPI 3.1 §4.8.8.2: "Templated paths with the
// same hierarchy but different templated names MUST NOT exist as they are
// identical." Tooling either rejects the document or silently drops one of the
// operations, so this cannot be left to review.
func TestNoDuplicatePathTemplates(t *testing.T) {
	specData, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("read spec %s: %v", specFile, err)
	}
	var spec map[string]interface{}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse spec %s: %v", specFile, err)
	}
	paths, _ := spec["paths"].(map[string]interface{})
	if len(paths) == 0 {
		t.Fatal("no paths in spec")
	}

	param := regexp.MustCompile(`\{[^}]*\}`)
	byTemplate := make(map[string][]string)
	for p := range paths {
		norm := param.ReplaceAllString(p, "{}")
		byTemplate[norm] = append(byTemplate[norm], p)
	}
	for norm, group := range byTemplate {
		if len(group) > 1 {
			sort.Strings(group)
			t.Errorf("paths %v are the same template %q — OpenAPI 3.1 §4.8.8.2 forbids declaring both", group, norm)
		}
	}
}
