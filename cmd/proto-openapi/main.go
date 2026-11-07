// Copyright Authova contributors. SPDX-License-Identifier: Apache-2.0
// proto-openapi derives the legacy-compatible OpenAPI projection from Proto.
// google.api.http owns paths; message/field descriptors own structural types.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/carvebloom/authova/api/authova/wire"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

type object = map[string]any

func fragment(m proto.Message, x protoreflect.ExtensionType) object {
	if !proto.HasExtension(m, x) {
		return object{}
	}
	raw := proto.GetExtension(m, x).(string)
	v := object{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		panic(err)
	}
	return v
}
func schemaName(m protoreflect.MessageDescriptor) string {
	meta := fragment(m.Options(), wire.E_Schema)
	if n, ok := meta["x-authova-schema-name"].(string); ok {
		return n
	}
	return string(m.Name())
}
func scalar(f protoreflect.FieldDescriptor) object {
	switch f.Kind() {
	case protoreflect.StringKind:
		return object{"type": "string"}
	case protoreflect.BoolKind:
		return object{"type": "boolean"}
	case protoreflect.Int64Kind, protoreflect.Int32Kind:
		return object{"type": "integer"}
	case protoreflect.DoubleKind, protoreflect.FloatKind:
		return object{"type": "number"}
	case protoreflect.MessageKind:
		if proto.HasExtension(f.Message().Options(), wire.E_NullableString) {
			return object{"type": "string"}
		}
		if f.Message().FullName() == "google.protobuf.Struct" {
			return object{"type": "object"}
		}
		return object{"$ref": "#/components/schemas/" + schemaName(f.Message())}
	default:
		panic(fmt.Sprintf("unsupported public field %s", f.FullName()))
	}
}
func property(f protoreflect.FieldDescriptor) object {
	v := fragment(f.Options(), wire.E_Property)
	nullable, _ := v["x-authova-nullable"].(bool)
	delete(v, "x-authova-nullable")
	list := f.IsList()
	element := f
	if f.Message() != nil && proto.HasExtension(f.Message().Options(), wire.E_UnwrappedList) && proto.GetExtension(f.Message().Options(), wire.E_UnwrappedList).(bool) {
		list = true
		element = f.Message().Fields().ByName("values")
	}
	if list {
		v["type"] = "array"
		item := fragment(f.Options(), wire.E_Item)
		for k, x := range scalar(element) {
			item[k] = x
		}
		v["items"] = item
	} else {
		for k, x := range scalar(f) {
			v[k] = x
		}
	}
	if nullable {
		v["type"] = []string{v["type"].(string), "null"}
	}
	return v
}
func message(m protoreflect.MessageDescriptor) object {
	v := fragment(m.Options(), wire.E_Schema)
	delete(v, "x-authova-schema-name")
	v["type"] = "object"
	props := object{}
	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		if hidden, _ := fragment(f.Options(), wire.E_Property)["x-authova-undocumented"].(bool); hidden {
			continue
		}
		props[f.JSONName()] = property(f)
	}
	v["properties"] = props
	return v
}
func document(file protoreflect.FileDescriptor, files *protoregistry.Files) object {
	doc := fragment(file.Options(), wire.E_Document)
	paths := object{}
	schemas := object{}
	for i := 0; i < file.Services().Len(); i++ {
		service := file.Services().Get(i)
		for j := 0; j < service.Methods().Len(); j++ {
			method := service.Methods().Get(j)
			op := fragment(method.Options(), wire.E_Operation)
			if selected, ok := op["x-authova-document-schemas"].([]any); ok {
				for _, name := range selected {
					n := name.(string)
					var found protoreflect.MessageDescriptor
					// A query-local narrowed schema takes priority over an imported one.
					for k := 0; k < file.Messages().Len(); k++ {
						m := file.Messages().Get(k)
						if proto.HasExtension(m.Options(), wire.E_Schema) && schemaName(m) == n {
							found = m
							break
						}
					}
					if found == nil {
						f, err := files.FindFileByPath("api/authova/v1/management.proto")
						if err != nil {
							panic(err)
						}
						found = f.Messages().ByName(protoreflect.Name(n))
					}
					if found == nil {
						panic("schema missing: " + n)
					}
					schemas[n] = message(found)
				}
				delete(op, "x-authova-document-schemas")
			}
			params := []any{}
			for k := 0; k < method.Input().Fields().Len(); k++ {
				field := method.Input().Fields().Get(k)
				if field.Name() == "body" {
					body := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
					body["schema"] = scalar(field)
					continue
				}
				param := fragment(field.Options(), wire.E_Parameter)
				if hidden, _ := param["x-authova-undocumented"].(bool); hidden {
					continue
				}
				param["schema"] = property(field)
				params = append(params, param)
			}
			if len(params) > 0 {
				op["parameters"] = params
			}
			if method.Output().FullName() != "google.protobuf.Empty" {
				for code, response := range op["responses"].(map[string]any) {
					if strings.HasPrefix(code, "2") {
						response.(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"] = object{"$ref": "#/components/schemas/" + schemaName(method.Output())}
					}
				}
			}
			rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
			var verb, path string
			switch p := rule.Pattern.(type) {
			case *annotations.HttpRule_Get:
				verb, path = "get", p.Get
			case *annotations.HttpRule_Post:
				verb, path = "post", p.Post
			case *annotations.HttpRule_Patch:
				verb, path = "patch", p.Patch
			case *annotations.HttpRule_Put:
				verb, path = "put", p.Put
			case *annotations.HttpRule_Delete:
				verb, path = "delete", p.Delete
			default:
				panic("unsupported HTTP rule")
			}
			if paths[path] == nil {
				paths[path] = object{}
			}
			paths[path].(map[string]any)[verb] = op
		}
	}
	doc["paths"] = paths
	doc["components"].(map[string]any)["schemas"] = schemas
	return doc
}
func main() {
	out := flag.String("output", "contracts/openapi/v1", "output directory")
	descriptor := flag.String("descriptor", "", "protoc descriptor set")
	flag.Parse()
	data, err := os.ReadFile(*descriptor)
	if err != nil {
		panic(err)
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err = proto.Unmarshal(data, set); err != nil {
		panic(err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		panic(err)
	}
	for _, name := range []string{"management", "query"} {
		file, err := files.FindFileByPath("api/authova/v1/" + name + ".proto")
		if err != nil {
			panic(err)
		}
		data, err := json.MarshalIndent(document(file, files), "", "  ")
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(filepath.Join(*out, name+".yaml"), append(data, '\n'), 0644); err != nil {
			panic(err)
		}
	}
}
