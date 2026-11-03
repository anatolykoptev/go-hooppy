package main

import (
	"fmt"
	"reflect"

	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
)

// RegisterOpFlags registers one cobra flag per `flag`-tagged field of the
// ops input struct type t — the flag name from the `flag` tag, the help
// text from `flagusage`. Supported field kinds: int, string, bool, []int,
// and pointers to those; pointer-ness does not change the flag's type,
// only how FillOpInput treats "flag not given". Fields tagged `flag:"-"`
// are explicit opt-outs and fields with no `flag` tag are skipped here —
// the parity test requires every input field to declare one or the other.
//
// The flags are registered without backing variables: values are read back
// through the FlagSet by FillOpInput after parsing, which is what lets a
// pointer field distinguish "flag absent" from "flag given as zero".
func RegisterOpFlags(cmd *cobra.Command, t reflect.Type) error {
	fs := cmd.Flags()
	for _, f := range ops.InputFields(t) {
		if f.FlagName == "" || f.FlagName == "-" {
			continue
		}
		switch base := flagBaseType(f.Type); {
		case base.Kind() == reflect.Int:
			fs.Int(f.FlagName, 0, f.FlagUsage)
		case base.Kind() == reflect.String:
			fs.String(f.FlagName, "", f.FlagUsage)
		case base.Kind() == reflect.Bool:
			fs.Bool(f.FlagName, false, f.FlagUsage)
		case isIntSlice(base):
			fs.IntSlice(f.FlagName, nil, f.FlagUsage)
		default:
			return fmt.Errorf("opflags: %s.%s has flag %q but unsupported type %s (want int, string, bool, []int or a pointer to one)", t, f.GoName, f.FlagName, f.Type)
		}
	}
	return nil
}

// FillOpInput populates the ops input struct dst (a pointer to the struct
// type flags were registered from) from cmd's parsed flags. A pointer
// field is set ONLY when its flag was explicitly given on the command line
// — nil is the "not given" marker ops' presence semantics rely on, and an
// explicit `--delete-after-days 0` must survive as a pointer to 0. Value
// fields always take the flag's value (the flag default when not given).
func FillOpInput(cmd *cobra.Command, dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("opflags: dst must be a non-nil pointer to a struct, got %T", dst)
	}
	v = v.Elem()
	fs := cmd.Flags()
	for _, f := range ops.InputFields(v.Type()) {
		if f.FlagName == "" || f.FlagName == "-" {
			continue
		}
		isPtr := f.Type.Kind() == reflect.Pointer
		if isPtr && !fs.Changed(f.FlagName) {
			continue
		}
		base := f.Type
		if isPtr {
			base = base.Elem()
		}
		var val reflect.Value
		var err error
		switch {
		case base.Kind() == reflect.Int:
			var n int
			n, err = fs.GetInt(f.FlagName)
			val = reflect.ValueOf(n)
		case base.Kind() == reflect.String:
			var s string
			s, err = fs.GetString(f.FlagName)
			val = reflect.ValueOf(s)
		case base.Kind() == reflect.Bool:
			var b bool
			b, err = fs.GetBool(f.FlagName)
			val = reflect.ValueOf(b)
		case isIntSlice(base):
			var ids []int
			ids, err = fs.GetIntSlice(f.FlagName)
			val = reflect.ValueOf(ids)
		default:
			return fmt.Errorf("opflags: %s has flag %q but unsupported type %s", v.Type(), f.GoName, f.Type)
		}
		if err != nil {
			return fmt.Errorf("opflags: read --%s: %w", f.FlagName, err)
		}
		fv := v.FieldByIndex(f.Index)
		if isPtr {
			p := reflect.New(base)
			p.Elem().Set(val)
			fv.Set(p)
		} else {
			fv.Set(val)
		}
	}
	return nil
}

// flagBaseType strips one level of pointer from a field type — the flag's
// registered type is the pointed-at kind.
func flagBaseType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

func isIntSlice(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Int
}
