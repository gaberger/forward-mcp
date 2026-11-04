package certified

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/forward-mcp/internal/domain/entities"
)

// CheckOutput compares result rows against the entry's output_schema and
// returns human-readable drift warnings. It is deliberately a shape check
// (required keys, simple property types) rather than a full JSON Schema
// validator, so it needs no dependency; drift is a warning, never an error,
// because Forward may add columns between library releases.
//
// Only the first sampleRows rows are inspected.
func CheckOutput(q entities.CertifiedQuery, rows []map[string]any, sampleRows int) []string {
	if len(rows) == 0 || len(q.OutputSchema) == 0 {
		return nil
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(q.OutputSchema, &schema); err != nil {
		return []string{"output_schema could not be parsed: " + err.Error()}
	}
	types := map[string][]string{}
	for name, raw := range schema.Properties {
		var p struct {
			Type any `json:"type"`
		}
		if json.Unmarshal(raw, &p) == nil {
			types[name] = typeList(p.Type)
		}
	}
	if sampleRows <= 0 || sampleRows > len(rows) {
		sampleRows = len(rows)
	}
	seen := map[string]bool{}
	var warnings []string
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if !seen[msg] {
			seen[msg] = true
			warnings = append(warnings, msg)
		}
	}
	for i := 0; i < sampleRows; i++ {
		row := rows[i]
		for _, req := range schema.Required {
			if _, ok := row[req]; !ok {
				warn("row missing required column %q", req)
			}
		}
		for name, allowed := range types {
			v, ok := row[name]
			if !ok || len(allowed) == 0 {
				continue
			}
			if !matchesAny(v, allowed) {
				warn("column %q: expected %v, got %s", name, allowed, jsonTypeOf(v))
			}
		}
	}
	return warnings
}

func typeList(t any) []string {
	switch x := t.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func jsonTypeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case int, int64, int32:
		return "integer"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func matchesAny(v any, allowed []string) bool {
	got := jsonTypeOf(v)
	for _, a := range allowed {
		if a == got || (a == "number" && got == "integer") {
			return true
		}
	}
	return false
}
