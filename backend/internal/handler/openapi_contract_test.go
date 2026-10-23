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

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
)

var ginParameter = regexp.MustCompile(`[:*]([A-Za-z][A-Za-z0-9_]*)`)

// First adoption slice: Agent's public protocol. Internal worker callbacks are
// mounted separately and intentionally have no public OpenAPI contract.
func governedOpenAPIRoute(key string) bool {
	parts := strings.SplitN(key, " ", 2)
	if len(parts) != 2 {
		return false
	}
	return strings.HasPrefix(parts[1], "/api/agent/") || strings.HasPrefix(parts[1], "/api/admin/settings/agent-")
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
			if upper == "GET" || upper == "POST" || upper == "PUT" || upper == "PATCH" || upper == "DELETE" {
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
	routes := gin.RoutesInfo{{Method: "GET", Path: "/api/agent/things/:id"}, {Method: "POST", Path: "/api/agent/hidden"}}
	missing, stale, err := openAPIRouteDiff([]byte("paths:\n  /agent/things/{id}:\n    get: {}\n  /agent/gone:\n    put: {}\n"), routes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(missing, ",") != "POST /api/agent/hidden" || strings.Join(stale, ",") != "PUT /api/agent/gone" {
		t.Fatalf("missing=%v stale=%v", missing, stale)
	}
}

func schemaFieldsMatchJSONTags(properties map[string]any, required []string, value any) error {
	typ := reflect.TypeOf(value)
	tags := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			tags[name] = true
		}
	}
	for name := range properties {
		if !tags[name] {
			return fmt.Errorf("schema property %q has no JSON field on %s", name, typ.Name())
		}
	}
	for _, name := range required {
		if _, ok := properties[name]; !ok {
			return fmt.Errorf("required property %q is undefined", name)
		}
	}
	return nil
}

func TestCloudAgentRequestSchemaMatchesJSONTags(t *testing.T) {
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `yaml:"properties"`
				Required   []string       `yaml:"required"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatal(err)
	}
	schema, ok := document.Components.Schemas["CloudAgentRequest"]
	if !ok || len(schema.Properties) == 0 {
		t.Fatal("CloudAgentRequest schema missing")
	}
	if err := schemaFieldsMatchJSONTags(schema.Properties, schema.Required, service.CloudAgentRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaTagGuardFixture(t *testing.T) {
	if err := schemaFieldsMatchJSONTags(map[string]any{"missing": true}, []string{"missing"}, service.CloudAgentRequest{}); err == nil {
		t.Fatal("unknown JSON property was accepted")
	}
}
