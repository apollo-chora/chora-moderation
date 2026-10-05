// Ported from google.golang.org/adk/server/agentengine/internal/helper
// (Copyright 2026 Google LLC, Apache License 2.0). The snake_case
// projection is load-bearing for the SSE wire format: session.Event and
// its nested model types carry no JSON tags, and callers parse the
// snake_case field names.

package agentserver

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"
)

// convertSnake returns an object corresponding the input object. It uses
// simple types, map[string]any and []any to reflect the input. All names
// are converted to snake case ('RequestedToolConfirmations' becomes
// 'requested_tool_confirmations'). json tags are supported, as is struct
// embedding.
func convertSnake(o any) any {
	res, err := convertSnakeInner("", "", o)
	if err != nil {
		log.Printf("Failed to convert: %+v of type %T: %v", o, o, err)
		// better to return an original version than nothing
		return o
	}
	return res
}

func convertSnakeInner(path, indent string, o any) (any, error) {
	if o == nil {
		return nil, nil
	}
	v := reflect.ValueOf(o)
	switch v.Kind() {
	case reflect.String:
		s, ok := o.(string)
		if !ok {
			return o, nil
		}
		return s, nil
	case reflect.Struct:
		vt := v.Type()
		if vt == reflect.TypeOf(time.Time{}) {
			t := o.(time.Time)
			return t.UnixMilli() / 1000.0, nil
		}

		m := make(map[string]any)
		for i := 0; i < v.NumField(); i++ {
			fv := v.Field(i)
			fvt := vt.Field(i)
			tag := fvt.Tag.Get("json")
			name, omitEmpty, omitZero, skip, err := fieldName(fvt.Name, tag)
			if err != nil {
				return nil, fmt.Errorf("failed to parse tag (%v): %w", tag, err)
			}
			if skip {
				continue
			}
			if fvt.Anonymous {
				embed, err := convertSnakeInner(path+"."+name, indent+".   ", fv.Interface())
				if err != nil {
					return nil, fmt.Errorf("failed to convert embedded struct with name:%v o: %+v %T err: %w", name, fv.Interface(), fv.Interface(), err)
				}
				for k, v := range embed.(map[string]any) {
					m[k] = v
				}
				continue
			}

			newPath := path + "." + name
			newName := convertName(newPath, name)
			if fv.CanInterface() {
				val, err := convertSnakeInner(newPath, indent+".   ", fv.Interface())
				if err != nil {
					return nil, fmt.Errorf("failed to convert regular struct field with path: %v err: %w", newPath, err)
				}
				if omitEmpty {
					if val != nil {
						addIfNotEmpty(val, m, newName)
					}
				} else {
					if val != nil {
						m[newName] = val
					}
				}
			} else {
				val := convertValue(fv)
				if val != 0 || !omitZero {
					m[newName] = val
				}
			}
		}
		return m, nil
	case reflect.Slice:
		res := []any{}
		for i := 0; i < v.Len(); i++ {
			elem, err := convertSnakeInner(path+".[]", indent+"    ", v.Index(i).Interface())
			if err != nil {
				return nil, fmt.Errorf("failed to convert slice element with path: %v err: %w", path+".[]", err)
			}
			res = append(res, elem)
		}
		if len(res) == 0 {
			return []any{}, nil
		}
		return res, nil
	case reflect.Map:
		res := make(map[string]any)
		for _, k := range v.MapKeys() {
			elem, err := convertSnakeInner(path+"->", indent+"    ", v.MapIndex(k).Interface())
			if err != nil {
				return nil, fmt.Errorf("failed to convert map element with path: %v err: %w", path+"->", err)
			}
			res[k.String()] = elem
		}
		if len(res) == 0 {
			return map[string]any{}, nil
		}
		return res, nil
	case reflect.Ptr:
		if v.IsNil() {
			return nil, nil
		}
		return convertSnakeInner(path+"*", indent+"    ", v.Elem().Interface())
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil

	default:
		return nil, fmt.Errorf("unsupported type: %v", v.Kind())
	}
}

func addIfNotEmpty(val any, m map[string]any, newName string) {
	switch t := val.(type) {
	case map[string]any:
		if len(t) != 0 {
			m[newName] = val
		}
	case []any:
		if len(t) != 0 {
			m[newName] = val
		}
	case bool:
		if t {
			m[newName] = val
		}
	case string:
		if t != "" {
			m[newName] = val
		}
	default:
		m[newName] = val
	}
}

// pathToName allows to provide a list of exceptions for a known input
// structures: key is a path for a name to be converted, value is its custom
// replacement.
var pathToName = map[string]string{
	".LongRunningToolIDs": "long_running_tool_ids",
}

// convertName converts a name to snake case.
func convertName(path, name string) string {
	if res, ok := pathToName[path]; ok {
		return res
	}

	l := strings.ToLower(name)
	b := &strings.Builder{}
	afterUnderscore := true
	for i := 0; i < len(name); i++ {
		// Ab  => _ab
		if !afterUnderscore && i > 0 && i+1 < len(name) && name[i] != l[i] && name[i+1] == l[i+1] {
			fmt.Fprintf(b, "_%c", l[i])
			afterUnderscore = true
			continue
		}
		// aB  => a_b
		if !afterUnderscore && i+1 < len(name) && name[i] == l[i] && name[i+1] != l[i+1] {
			fmt.Fprintf(b, "%c_", l[i])
			afterUnderscore = true
			continue
		}
		afterUnderscore = false
		fmt.Fprintf(b, "%c", l[i])
	}
	return b.String()
}

// parseTag handles json tags. Accepted format is comma-separated list of
// strings: "-", "omitempty" and "omitzero" are recognized.
func parseTag(tag string) (name string, omitEmpty, omitZero, skip bool, err error) {
	if tag == "" {
		return "", false, false, false, nil
	}
	if tag == "-" {
		return "", false, false, true, nil
	}
	vals := strings.Split(tag, ",")
	for _, val := range vals {
		if val == "" {
			continue
		}
		switch val {
		case "omitempty":
			if omitEmpty {
				return "", false, false, false, fmt.Errorf("duplicate omitempty")
			}
			omitEmpty = true
		case "omitzero":
			if omitZero {
				return "", false, false, false, fmt.Errorf("duplicate omitzero")
			}
			omitZero = true
		default:
			if name != "" {
				return "", false, false, false, fmt.Errorf("duplicate name")
			}
			name = val
		}
	}
	return name, omitEmpty, omitZero, skip, nil
}

// fieldName returns a name for the field after the json tag is taken into
// consideration.
func fieldName(name, tag string) (newName string, omitEmpty, omitZero, skip bool, err error) {
	newName, omitEmpty, omitZero, skip, err = parseTag(tag)
	if newName == "" {
		newName = name
	}
	return newName, omitEmpty, omitZero, skip, err
}

// convertValue handles String, Int, Uint and Float.
func convertValue(o reflect.Value) any {
	if o.CanInt() {
		return o.Int()
	}
	if o.CanUint() {
		return o.Uint()
	}
	if o.CanFloat() {
		return o.Float()
	}
	if o.Kind() == reflect.String {
		return o.String()
	}
	return o
}
