// Generates the renderer contract from the actual Go wire types.
package main

import (
	"fmt"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/mediaversions"
	"infinite-canvas/backend/internal/canvas/tasks"
	"os"
	"reflect"
	"sort"
	"strings"
)

var seen = map[reflect.Type]string{}
var output = map[string]string{}

func name(t reflect.Type) string {
	switch t.Name() {
	case "canvasDrawingOp":
		return "DrawingOperation"
	case "canvasPrevisPatchOperation":
		return "PrevisOperation"
	}
	return t.Name()
}
func ts(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return ts(t.Elem())
	}
	if t.Name() == "Document" {
		return "Record<string, unknown>"
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return "string"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64, reflect.Float64:
		return "number"
	case reflect.Interface:
		return "unknown"
	case reflect.Map:
		return "Record<string, " + ts(t.Elem()) + ">"
	case reflect.Slice:
		return "Array<" + ts(t.Elem()) + ">"
	case reflect.Struct:
		if s, ok := seen[t]; ok {
			return s
		}
		n := name(t)
		seen[t] = n
		fields := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			key := strings.Split(tag, ",")[0]
			if key == "" || key == "-" {
				continue
			}
			optional := ""
			if strings.Contains(tag, "omitempty") || f.Type.Kind() == reflect.Pointer {
				optional = "?"
			}
			value := ts(f.Type)
			if f.Type.Kind() == reflect.Pointer && !strings.Contains(tag, "omitempty") {
				value += " | null"
			}
			if n == "Operation" && key == "type" {
				value = "OperationType"
			}
			if key == "protocolVersion" {
				value = "1"
			}
			fields = append(fields, fmt.Sprintf("  %s%s: %s;", key, optional, value))
		}
		output[n] = "export interface " + n + " {\n" + strings.Join(fields, "\n") + "\n}"
		return n
	default:
		panic(t.String())
	}
}
func main() {
	path := "../web/src/lib/canvas/command-contract.generated.ts"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	for _, v := range []any{commands.Command{}, commands.Operation{}, commands.ChangeSet{}, commands.Snapshot{}, commands.Receipt{}, commands.CreateCanvasRequest{}, tasks.TaskRequest{}, tasks.Consumer{}, tasks.SubmitRequest{}, tasks.PrevisSubmitRequest{}, tasks.TaskHandle{}, tasks.MaterializationStatus{}, mediaversions.Page{}} {
		ts(reflect.TypeOf(v))
	}
	names := []string{}
	for n := range output {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("// Generated from Go canvas command/task contracts. From backend: go run ./internal/canvas/commands/generate\nexport const CANVAS_COMMAND_PROTOCOL = 1 as const;\nexport type OperationType = ")
	ops := []string{}
	for _, op := range commands.OperationTypes {
		ops = append(ops, fmt.Sprintf("%q", op))
	}
	b.WriteString(strings.Join(ops, " | ") + ";\n\n")
	for _, n := range names {
		b.WriteString(output[n] + "\n\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		panic(err)
	}
}
