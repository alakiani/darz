package coordinator

import (
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The OpenAPI description of the Admin API listener is generated from the
// route table and the protocol types, and checked in as openapi.json; a
// test fails when the file is stale. The panel's TypeScript client is
// generated from that file (session 7).

var pathParamRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// OpenAPI returns the API's OpenAPI 3.1 description.
func (c *Coordinator) OpenAPI() ([]byte, error) {
	schemas := map[string]any{}
	paths := map[string]map[string]any{}
	for _, r := range c.routes() {
		if r.path == "/v1/openapi.json" {
			continue
		}
		op := map[string]any{"summary": r.summary, "operationId": operationID(r)}
		switch r.auth {
		case authToken:
			op["security"] = []any{map[string]any{"apiToken": []any{}}}
		case authUser, authAdmin:
			op["security"] = []any{map[string]any{"session": []any{}}}
		default:
			op["security"] = []any{}
		}
		if r.auth == authAdmin {
			op["description"] = "Admins only, with a session that proved TOTP."
		}
		var params []any
		for _, m := range pathParamRE.FindAllStringSubmatch(r.path, -1) {
			params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if params != nil {
			op["parameters"] = params
		}
		if r.request != nil {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{
				"application/json": map[string]any{"schema": schemaOf(reflect.TypeOf(r.request), schemas)}}}
		}
		responses := map[string]any{
			"default": map[string]any{"description": "An error", "content": map[string]any{
				"application/json": map[string]any{"schema": schemaOf(reflect.TypeOf(errorBody{}), schemas)}}},
		}
		if r.response != nil {
			responses["200"] = map[string]any{"description": "OK", "content": map[string]any{
				"application/json": map[string]any{"schema": schemaOf(reflect.TypeOf(r.response), schemas)}}}
		} else {
			responses["204"] = map[string]any{"description": "Done"}
		}
		op["responses"] = responses
		if paths[r.path] == nil {
			paths[r.path] = map[string]any{}
		}
		paths[r.path][strings.ToLower(r.method)] = op
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "DARZ Coordinator API", "version": "1",
			"description": "The panel's and the enroll playbook's API. Changes must be application/json."},
		"paths": paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"session":  map[string]any{"type": "apiKey", "in": "cookie", "name": sessionCookie},
				"apiToken": map[string]any{"type": "http", "scheme": "bearer"},
			},
		},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n'), err
}

// errorBody mirrors protocol.Error for the spec.
type errorBody struct {
	Error string `json:"error"`
}

func operationID(r route) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(r.method))
	for _, part := range strings.Split(strings.TrimPrefix(r.path, "/v1/"), "/") {
		part = strings.Trim(part, "{}")
		for _, w := range strings.FieldsFunc(part, func(r rune) bool { return r == '-' || r == '_' }) {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

var timeType = reflect.TypeOf(time.Time{})

// schemaOf turns a Go type into a JSON Schema, adding named structs to
// schemas and referring to them.
func schemaOf(t reflect.Type, schemas map[string]any) map[string]any {
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t.Kind() == reflect.Pointer:
		return schemaOf(t.Elem(), schemas)
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), schemas)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), schemas)}
	case reflect.Struct:
		name := t.Name()
		if name == "errorBody" {
			name = "Error"
		}
		if _, ok := schemas[name]; !ok {
			schemas[name] = nil // placeholder against recursion
			props := map[string]any{}
			var required []string
			addFields(t, props, &required, schemas)
			sort.Strings(required)
			s := map[string]any{"type": "object", "properties": props}
			if len(required) > 0 {
				s["required"] = required
			}
			schemas[name] = s
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

func addFields(t reflect.Type, props map[string]any, required *[]string, schemas map[string]any) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			addFields(f.Type, props, required, schemas)
			continue
		}
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		props[name] = schemaOf(f.Type, schemas)
		if !strings.Contains(opts, "omitempty") && f.Type.Kind() != reflect.Pointer {
			*required = append(*required, name)
		}
	}
}

func (c *Coordinator) serveSpec(w http.ResponseWriter, r *http.Request) {
	b, err := c.OpenAPI()
	if err != nil {
		c.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}
