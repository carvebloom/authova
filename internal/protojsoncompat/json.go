// Package protojsoncompat preserves Authova's existing JSON wire contract while
// exposing generated protobuf messages to Kratos services. It deliberately uses
// numeric int64 and snake_case, preserves false/empty/null/presence, and rejects
// unknown fields and quoted integers. It does not replace protocol encoders.
package protojsoncompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/carvebloom/authova/api/authova/wire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func marked(m protoreflect.MessageDescriptor, option protoreflect.ExtensionType) bool {
	return proto.HasExtension(m.Options(), option) && proto.GetExtension(m.Options(), option).(bool)
}
func Marshal(m proto.Message) ([]byte, error) { return json.Marshal(message(m.ProtoReflect())) }
func message(m protoreflect.Message) any {
	if marked(m.Descriptor(), wire.E_UnwrappedList) {
		if m.Get(m.Descriptor().Fields().ByName("is_null")).Bool() {
			return nil
		}
		values := m.Get(m.Descriptor().Fields().ByName("values")).List()
		out := make([]any, values.Len())
		f := m.Descriptor().Fields().ByName("values")
		for i := range out {
			out[i] = scalar(f, values.Get(i))
		}
		return out
	}
	if marked(m.Descriptor(), wire.E_NullableString) {
		if m.Get(m.Descriptor().Fields().ByName("is_null")).Bool() {
			return nil
		}
		return m.Get(m.Descriptor().Fields().ByName("value")).String()
	}
	out := map[string]any{}
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.IsList() {
			values := v.List()
			items := make([]any, values.Len())
			for i := range items {
				items[i] = scalar(f, values.Get(i))
			}
			out[f.JSONName()] = items
		} else {
			out[f.JSONName()] = scalar(f, v)
		}
		return true
	})
	return out
}
func scalar(f protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch f.Kind() {
	case protoreflect.MessageKind:
		return message(v.Message())
	case protoreflect.Int64Kind, protoreflect.Int32Kind:
		return json.Number(strconv.FormatInt(v.Int(), 10))
	case protoreflect.StringKind:
		return v.String()
	case protoreflect.BoolKind:
		return v.Bool()
	case protoreflect.DoubleKind, protoreflect.FloatKind:
		return v.Float()
	default:
		panic("unsupported public Proto field: " + string(f.FullName()))
	}
}
func Unmarshal(data []byte, m proto.Message) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value, err := readValue(d)
	if err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	proto.Reset(m)
	return fill(m.ProtoReflect(), value)
}

type jsonField struct {
	key   string
	value any
}
type jsonObject []jsonField

// Keep object order: encoding/json applies case-insensitive aliases in input
// order. A map would randomize {"Name":"first","name":"last"}.
func readValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := jsonObject{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			value, err := readValue(d)
			if err != nil {
				return nil, err
			}
			object = append(object, jsonField{key.(string), value})
		}
		_, err = d.Token()
		return object, err
	case '[':
		array := []any{}
		for d.More() {
			value, err := readValue(d)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err = d.Token()
		return array, err
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}
func fill(m protoreflect.Message, value any) error {
	fields := m.Descriptor().Fields()
	if marked(m.Descriptor(), wire.E_UnwrappedList) {
		if value == nil {
			m.Set(fields.ByName("is_null"), protoreflect.ValueOfBool(true))
			return nil
		}
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected array")
		}
		f := fields.ByName("values")
		list := m.Mutable(f).List()
		for _, item := range array {
			v, err := convert(f, item, list.NewElement())
			if err != nil {
				return err
			}
			list.Append(v)
		}
		return nil
	}
	if marked(m.Descriptor(), wire.E_NullableString) {
		if value == nil {
			m.Set(fields.ByName("is_null"), protoreflect.ValueOfBool(true))
			return nil
		}
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected string or null")
		}
		m.Set(fields.ByName("value"), protoreflect.ValueOfString(s))
		return nil
	}
	if value == nil {
		return nil
	}
	object, ok := value.(jsonObject)
	if !ok {
		return fmt.Errorf("expected object")
	}
	for _, item := range object {
		key, raw := item.key, item.value
		var f protoreflect.FieldDescriptor
		for i := 0; i < fields.Len(); i++ {
			candidate := fields.Get(i)
			if strings.EqualFold(key, candidate.JSONName()) {
				f = candidate
				break
			}
		}
		if f == nil {
			return fmt.Errorf("unknown field %q", key)
		}
		wrapped := f.Message() != nil && (marked(f.Message(), wire.E_UnwrappedList) || marked(f.Message(), wire.E_NullableString))
		if raw == nil && !wrapped {
			// encoding/json clears pointers but ignores null for value scalars.
			if f.Message() != nil || (proto.HasExtension(f.Options(), wire.E_NullClears) && proto.GetExtension(f.Options(), wire.E_NullClears).(bool)) {
				m.Clear(f)
			}
			continue
		}
		value := m.NewField(f)
		if f.Message() != nil && !wrapped && m.Has(f) {
			// Duplicate objects merge into an existing Go struct pointer. Lists
			// and nullable wrappers instead replace their previous wire value.
			value = m.Mutable(f)
		}
		v, err := convert(f, raw, value)
		if err != nil {
			return fmt.Errorf("field %s: %w", key, err)
		}
		m.Set(f, v)
	}
	return nil
}
func convert(f protoreflect.FieldDescriptor, value any, v protoreflect.Value) (protoreflect.Value, error) {
	switch f.Kind() {
	case protoreflect.MessageKind:
		if err := fill(v.Message(), value); err != nil {
			return v, err
		}
		return v, nil
	case protoreflect.StringKind:
		s, ok := value.(string)
		if ok {
			return protoreflect.ValueOfString(s), nil
		}
	case protoreflect.BoolKind:
		b, ok := value.(bool)
		if ok {
			return protoreflect.ValueOfBool(b), nil
		}
	case protoreflect.Int64Kind, protoreflect.Int32Kind:
		n, ok := value.(json.Number)
		if ok {
			i, err := n.Int64()
			if err != nil {
				return v, err
			}
			return protoreflect.ValueOfInt64(i), nil
		}
	case protoreflect.DoubleKind, protoreflect.FloatKind:
		n, ok := value.(json.Number)
		if ok {
			d, err := n.Float64()
			if err != nil {
				return v, err
			}
			return protoreflect.ValueOfFloat64(d), nil
		}
	}
	return v, fmt.Errorf("invalid JSON value for %s", f.FullName())
}

// FromDTO maps public application DTOs without importing Ent or storage types.
func FromDTO(value any, m proto.Message) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return Unmarshal(data, m)
}
