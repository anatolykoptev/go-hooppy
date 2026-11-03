// Package ops holds the one-operation layer shared by the hooppy CLI and
// the hooppy-mcp server: one exported function per operation owning all
// input validation, presence semantics and request building. Both surfaces
// are thin adapters over it — they add nothing but I/O.
//
// The input struct is the single source of the parameter set. Optional
// fields are pointers, so presence is explicit: nil means "not given" and a
// pointed-at zero is a real value (delete_posts_day=0 disables auto-delete;
// the old int-sentinel inputs could not express that). Struct tags carry
// `json` (the wire/MCP property name), `jsonschema` (the MCP description),
// `flag` (the CLI flag name) and `flagusage` (the CLI flag help text).
// Adding a field to an input exposes it on both surfaces with no second
// edit — the parity tests in cmd/hooppy and cmd/hooppy-mcp enforce that
// every field declares a flag (or is explicitly opted out with `flag:"-"`)
// and appears in the tool's input schema.
//
// ValidationError messages name fields by their CLI flag (--delete-after-days)
// when a `flag` tag is declared, falling back to the json name — the CLI
// prints them verbatim and the text stays consistent with `--help`.
package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// ValidationError marks a local input refusal — validation that fires
// BEFORE any request is built. Callers distinguish it from API errors via
// errors.As: the CLI prints it as a usage error, the MCP handler returns it
// as a tool error.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

// Field describes one field of an ops input struct as the surfaces see it.
type Field struct {
	GoName    string       // struct field name, e.g. "DeletePostsDay"
	JSONName  string       // `json` tag name, e.g. "delete_posts_day" ("" when untagged)
	FlagName  string       // `flag` tag value: the CLI flag name, "" when undeclared, "-" when opted out
	FlagUsage string       // `flagusage` tag value: the CLI flag help text
	Type      reflect.Type // field type; pointer-ness is preserved (presence lives in the pointer)
	Index     []int        // FieldByIndex path into the struct (length > 1 for promoted fields)
}

// InputFields lists the exported fields of an ops input struct type —
// the single parse of the flag/json tags the CLI flag binder, the parity
// tests and ops' own error messages all derive from. Anonymous struct
// fields are recursed into so embedded inputs work.
func InputFields(t reflect.Type) []Field {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return inputFields(t, nil)
}

func inputFields(t reflect.Type, prefix []int) []Field {
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		idx := append(append([]int{}, prefix...), i)
		if f.Anonymous {
			if f.Type.Kind() == reflect.Struct {
				out = append(out, inputFields(f.Type, idx)...)
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		out = append(out, Field{
			GoName:    f.Name,
			JSONName:  strings.Split(f.Tag.Get("json"), ",")[0],
			FlagName:  f.Tag.Get("flag"),
			FlagUsage: f.Tag.Get("flagusage"),
			Type:      f.Type,
			Index:     idx,
		})
	}
	return out
}

// displayName returns the user-facing name of a field: "--"+flag name when
// declared, else the json name, else the Go name.
func (f Field) displayName() string {
	switch {
	case f.FlagName != "" && f.FlagName != "-":
		return "--" + f.FlagName
	case f.JSONName != "":
		return f.JSONName
	default:
		return f.GoName
	}
}

// errFieldf builds a ValidationError for one named field of in's type,
// e.g. "--state must be one of 1, 2 or 3 (got 0)". The display name comes
// from the field's own tags, so a renamed flag renames its errors too.
func errFieldf(in any, goName, format string, args ...any) error {
	display := goName
	for _, f := range InputFields(reflect.TypeOf(in)) {
		if f.GoName == goName {
			display = f.displayName()
			break
		}
	}
	return &ValidationError{msg: display + " " + fmt.Sprintf(format, args...)}
}

// errNoFields builds the "at least one field is required" refusal, naming
// every field of in's type: "at least one of --name, --state or
// --delete-after-days is required".
func errNoFields(in any) error {
	var names []string
	for _, f := range InputFields(reflect.TypeOf(in)) {
		names = append(names, f.displayName())
	}
	last := len(names) - 1
	return &ValidationError{msg: "at least one of " + strings.Join(names[:last], ", ") + " or " + names[last] + " is required"}
}

// buildOverrides turns an ops input struct into the overrides map
// UpdateScheduleFromEdit-style helpers take: one json-named key per
// non-nil pointer field, carrying the pointed-at value marshalled to JSON.
// Nil pointers produce NO key — a key travels only when explicitly given,
// which is what makes presence-based updates safe for read-modify-write.
// Value (non-pointer) fields are always sent; `json:"-"` is honoured.
func buildOverrides(in any) (map[string]json.RawMessage, error) {
	v := reflect.ValueOf(in)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	t := v.Type()
	out := map[string]json.RawMessage{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if f.Anonymous {
			if f.Type.Kind() != reflect.Struct {
				continue
			}
			inner, err := buildOverrides(fv.Interface())
			if err != nil {
				return nil, err
			}
			for k, val := range inner {
				out[k] = val
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		val := fv
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			val = fv.Elem()
		}
		b, err := json.Marshal(val.Interface())
		if err != nil {
			return nil, fmt.Errorf("hooppy ops: marshal %q: %w", name, err)
		}
		out[name] = b
	}
	return out, nil
}
