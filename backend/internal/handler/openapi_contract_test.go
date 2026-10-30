package handler

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
)

var ginParameter = regexp.MustCompile(`[:*]([A-Za-z][A-Za-z0-9_]*)`)

// RegisterCanvasAPI is the public /api contract. Internal worker callbacks are
// mounted on /internal-agent independently and have a separate wire protocol.
func governedOpenAPIRoute(key string) bool {
	parts := strings.SplitN(key, " ", 2)
	if len(parts) != 2 {
		return false
	}
	return strings.HasPrefix(parts[1], "/api/")
}

func openAPIRouteDiff(spec []byte, routes gin.RoutesInfo) ([]string, []string, error) {
	var document struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &document); err != nil {
		return nil, nil, err
	}
	if len(document.Paths) == 0 {
		return nil, nil, fmt.Errorf("OpenAPI paths are empty")
	}
	documented := map[string]bool{}
	for path, methods := range document.Paths {
		for method := range methods {
			upper := strings.ToUpper(method)
			if upper == "GET" || upper == "POST" || upper == "PUT" || upper == "PATCH" || upper == "DELETE" || upper == "HEAD" || upper == "OPTIONS" || upper == "TRACE" {
				key := upper + " /api" + path
				if governedOpenAPIRoute(key) {
					documented[key] = true
				}
			}
		}
	}
	registered := map[string]bool{}
	for _, route := range routes {
		key := route.Method + " " + ginParameter.ReplaceAllString(route.Path, `{$1}`)
		if governedOpenAPIRoute(key) {
			registered[key] = true
		}
	}
	var missing, stale []string
	for key := range registered {
		if !documented[key] {
			missing = append(missing, key)
		}
	}
	for key := range documented {
		if !registered[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale, nil
}

func TestOpenAPIRouteContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), &service.Service{})
	missing, stale, err := openAPIRouteDiff(openAPISpec, router.Routes())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open("openapi-route-baseline.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	var expected []string
	for scan.Scan() {
		if line := strings.TrimSpace(scan.Text()); line != "" && !strings.HasPrefix(line, "#") {
			expected = append(expected, line)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(expected)
	if strings.Join(missing, "\n") != strings.Join(expected, "\n") {
		t.Errorf("undocumented Gin routes differ from baseline\nactual:\n%s\nexpected:\n%s", strings.Join(missing, "\n"), strings.Join(expected, "\n"))
	}
	if len(stale) != 0 {
		t.Errorf("OpenAPI methods with no registered Gin route: %v", stale)
	}
}

func TestOpenAPIRouteDiffFixtures(t *testing.T) {
	routes := gin.RoutesInfo{{Method: "GET", Path: "/api/things/:id"}, {Method: "POST", Path: "/api/hidden"}, {Method: "GET", Path: "/internal-agent/claim"}}
	missing, stale, err := openAPIRouteDiff([]byte("paths:\n  /things/{id}:\n    get: {}\n  /gone:\n    put: {}\n"), routes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(missing, ",") != "POST /api/hidden" || strings.Join(stale, ",") != "PUT /api/gone" {
		t.Fatalf("missing=%v stale=%v", missing, stale)
	}
}

type openAPIObjectSchema struct {
	Type       string                         `yaml:"type"`
	Properties map[string]openAPIObjectSchema `yaml:"properties"`
	Required   []string                       `yaml:"required"`
}

func schemaFieldsMatchJSONTags(schema openAPIObjectSchema, value any) error {
	return objectSchemaMatchesJSONTags(schema, reflect.TypeOf(value), false, reflect.TypeOf(value).Name())
}

func objectSchemaMatchesJSONTags(schema openAPIObjectSchema, typ reflect.Type, complete bool, location string) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("%s is not a Go struct", location)
	}
	tags := map[string]reflect.Type{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			tags[name] = field.Type
		}
	}
	for name, property := range schema.Properties {
		fieldType, ok := tags[name]
		if !ok {
			return fmt.Errorf("%s.%s has no Go JSON field", location, name)
		}
		if property.Type == "object" && len(property.Properties) != 0 {
			if err := objectSchemaMatchesJSONTags(property, fieldType, true, location+"."+name); err != nil {
				return err
			}
		}
	}
	for _, name := range schema.Required {
		if _, ok := schema.Properties[name]; !ok {
			return fmt.Errorf("%s: required property %q is undefined", location, name)
		}
	}
	if complete {
		for name := range tags {
			if _, ok := schema.Properties[name]; !ok {
				return fmt.Errorf("%s.%s Go JSON field is missing from schema", location, name)
			}
		}
	}
	return nil
}

func TestCloudAgentRequestSchemaMatchesJSONTags(t *testing.T) {
	var document struct {
		Components struct {
			Schemas map[string]openAPIObjectSchema `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatal(err)
	}
	schema, ok := document.Components.Schemas["CloudAgentRequest"]
	if !ok || len(schema.Properties) == 0 {
		t.Fatal("CloudAgentRequest schema missing")
	}
	if err := schemaFieldsMatchJSONTags(schema, service.CloudAgentRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaTagGuardFixture(t *testing.T) {
	if err := schemaFieldsMatchJSONTags(openAPIObjectSchema{Properties: map[string]openAPIObjectSchema{"missing": {}}}, service.CloudAgentRequest{}); err == nil {
		t.Fatal("unknown JSON property was accepted")
	}
	budget := openAPIObjectSchema{Type: "object", Properties: map[string]openAPIObjectSchema{
		"maxCredits": {}, "maxGenerationTasks": {}, "maxVideoSeconds": {}, "maxStepz": {},
	}}
	if err := schemaFieldsMatchJSONTags(openAPIObjectSchema{Properties: map[string]openAPIObjectSchema{"budget": budget}}, service.CloudAgentRequest{}); err == nil {
		t.Fatal("drifted nested budget field was accepted")
	}
}

func queryTagsMatchParameters(value any, parameters []struct {
	In   string `yaml:"in"`
	Name string `yaml:"name"`
}) error {
	want := map[string]bool{}
	for _, parameter := range parameters {
		if parameter.In == "query" {
			want[parameter.Name] = true
		}
	}
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("form"), ",")[0]
		if !want[name] {
			return fmt.Errorf("query DTO form field %q is absent from OpenAPI", name)
		}
		delete(want, name)
	}
	for name := range want {
		return fmt.Errorf("OpenAPI query %q is absent from DTO", name)
	}
	return nil
}

func TestAgentRunReadQueryMatchesOpenAPI(t *testing.T) {
	var document struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					In   string `yaml:"in"`
					Name string `yaml:"name"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatal(err)
	}
	parameters := document.Paths["/agent/runs/{id}"].Get.Parameters
	if len(parameters) == 0 {
		t.Fatal("Agent run read query schema missing")
	}
	if err := queryTagsMatchParameters(agentRunViewQuery{}, parameters); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunReadQueryTagFixture(t *testing.T) {
	parameters := []struct {
		In   string `yaml:"in"`
		Name string `yaml:"name"`
	}{{In: "query", Name: "sinceSeq"}}
	if err := queryTagsMatchParameters(agentRunViewQuery{}, parameters); err == nil {
		t.Fatal("missing eventLimit was accepted")
	}
}
