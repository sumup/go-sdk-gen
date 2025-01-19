package builder

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/iancoleman/strcase"

	"github.com/sumup/go-sdk-gen/internal/stringx"
)

// schemasToTypes converts openapi3 schemas to golang struct and enum types.
func (b *Builder) schemasToTypes(schemas []*openapi3.SchemaRef, errorSchemas map[string]struct{}) []Writable {
	var allTypes []Writable

	for _, s := range schemas {
		_, isErr := errorSchemas[s.Ref]
		name := strcase.ToCamel(strings.TrimPrefix(s.Ref, "#/components/schemas/"))
		typeTpl := generateSchemaComponents(name, s, isErr)
		allTypes = append(allTypes, typeTpl...)
	}

	return allTypes
}

// schemasToTypes converts openapi3 schemas to golang struct and enum types.
func (b *Builder) respToTypes(schemas []*openapi3.ResponseRef, errorSchemas map[string]struct{}) []Writable {
	var allTypes []Writable

	for _, s := range schemas {
		_, isErr := errorSchemas[s.Ref]
		name := strcase.ToCamel(strings.TrimPrefix(s.Ref, "#/components/responses/")) + "Response"
		if s.Value.Content == nil {
			if isErr {
				allTypes = append(allTypes, typeAssertionDeclaration{
					typ: name,
				})
			}

			var description string
			if s.Value.Description != nil {
				description = *s.Value.Description
			}

			allTypes = append(allTypes, &TypeDeclaration{
				Comment:  description,
				Type:     "struct{}",
				Name:     name,
				Response: s.Value,
			})
			if isErr {
				allTypes = append(allTypes, staticErrorImplementation{
					Typ:  name,
					Name: description,
				})
			}
			continue
		}
		typeTpl := generateSchemaComponents(name, s.Value.Content["application/json"].Schema, isErr)
		allTypes = append(allTypes, typeTpl...)
	}

	return allTypes
}

// TODO: is this different from respToTypes?
func (b *Builder) pathsToBodyTypes(paths *openapi3.Paths) []Writable {
	if paths == nil {
		return nil
	}

	paramTypes := make([]Writable, 0)
	for _, path := range paths.InMatchingOrder() {
		pathSpec := paths.Find(path)
		if pathSpec.Ref != "" {
			slog.Warn(fmt.Sprintf("TODO: skipping path for %q, since it is a reference", path))
			continue
		}

		operations := pathSpec.Operations()
		operationKeys := slices.Collect(maps.Keys(operations))
		slices.Sort(operationKeys)
		for _, method := range operationKeys {
			opSpec := operations[method]
			operationName := strcase.ToCamel(opSpec.OperationID)

			if opSpec.RequestBody != nil {
				mt, ok := opSpec.RequestBody.Value.Content["application/json"]
				if ok && mt.Schema != nil {
					name := operationName + "Body"
					bodyObject, additionalTypes := createObject(mt.Schema.Value, name)
					paramTypes = append(paramTypes, bodyObject)
					paramTypes = append(paramTypes, additionalTypes...)
				}
			}
		}
	}

	return paramTypes
}

// constructParamTypes constructs struct for query parameters for an operation.
func (b *Builder) pathsToParamTypes(paths *openapi3.Paths) []Writable {
	if paths == nil {
		return nil
	}

	paramTypes := make([]Writable, 0)

	for _, path := range paths.InMatchingOrder() {
		pathSpec := paths.Find(path)
		if pathSpec.Ref != "" {
			slog.Warn(fmt.Sprintf("TODO: skipping path for %q, since it is a reference", path))
			continue
		}

		operations := pathSpec.Operations()
		operationKeys := slices.Collect(maps.Keys(operations))
		slices.Sort(operationKeys)
		for _, method := range operationKeys {
			opSpec := operations[method]
			operationName := strcase.ToCamel(opSpec.OperationID)

			if len(opSpec.Parameters) > 0 {
				fields := make([]StructField, 0)
				for _, p := range opSpec.Parameters {
					// path parameters are passed as a parameters to the generated method
					if p.Value.In == "path" {
						continue
					}

					name := p.Value.Name
					if p.Ref != "" {
						name = strcase.ToCamel(strings.TrimPrefix(p.Ref, "#/components/schemas/"))
					}

					fields = append(fields, StructField{
						Name:      name,
						Type:      convertToValidGoType("", p.Value.Schema),
						Parameter: p.Value,
						Optional:  !p.Value.Required,
						Comment:   parameterPropertyGodoc(p.Value),
					})
				}

				if len(fields) != 0 {
					paramsTypeName := operationName + "Params"
					paramsTpl := TypeDeclaration{
						Type:      "struct",
						Name:      paramsTypeName,
						Comment:   operationParamsGodoc(paramsTypeName, opSpec),
						Fields:    fields,
						Operation: opSpec,
					}

					paramTypes = append(paramTypes, &paramsTpl, &toQueryValues{Typ: &paramsTpl})
				}
			}
		}
	}

	return paramTypes
}

// pathsToResponseTypes generates response types for operations. This is responsible only for inlined
// schemas that are specific to the operation itself and are not references.
func (b *Builder) pathsToResponseTypes(paths *openapi3.Paths) []Writable {
	if paths == nil {
		return nil
	}

	paramTypes := make([]Writable, 0)

	for _, path := range paths.InMatchingOrder() {
		pathSpec := paths.Find(path)
		if pathSpec.Ref != "" {
			slog.Warn(fmt.Sprintf("TODO: skipping path for %q, since it is a reference", path))
			continue
		}

		operations := pathSpec.Operations()
		operationKeys := slices.Collect(maps.Keys(operations))
		slices.Sort(operationKeys)
		for _, method := range operationKeys {
			opSpec := operations[method]
			operationName := strcase.ToCamel(opSpec.OperationID)

			responses := opSpec.Responses.Map()
			responseKeys := slices.Collect(maps.Keys(responses))
			slices.Sort(responseKeys)
			for _, code := range responseKeys {
				response := responses[code]

				if response.Ref != "" {
					ref := strings.TrimPrefix(response.Ref, "#/components/responses/")
					response = b.spec.Components.Responses[ref]
				}

				content, ok := response.Value.Content["application/json"]
				if !ok {
					continue
				}

				if content.Schema.Ref != "" {
					// schemas are handled separately, here we only care about inline schemas in the operation
					continue
				}

				name := getResponseName(operationName, code, content)
				isErr := code == "default" || strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5")

				objects := generateSchemaComponents(name, content.Schema, isErr)
				paramTypes = append(paramTypes, objects...)
			}
		}
	}

	return paramTypes
}

// generateSchemaComponents generates types from schema reference.
// This should be used to generate top-level types, that is - named schemas that are listed
// in `#/components/schemas/` part of the OpenAPI specs.
func generateSchemaComponents(name string, schema *openapi3.SchemaRef, isErr bool) []Writable {
	types := make([]Writable, 0)
	spec := schema.Value

	if isErr {
		types = append(types, typeAssertionDeclaration{
			typ: name,
		})
	}

	switch {
	case len(spec.Enum) > 0:
		enum := createEnum(spec, name)
		if enum != nil {
			types = append(types, enum)
		}
	case spec.Type.Is("string"):
		types = append(types, &TypeDeclaration{
			Comment: schemaGodoc(name, spec),
			Type:    "string",
			Name:    name,
			Schema:  spec,
		})
	case spec.Type.Is("integer"):
		types = append(types, &TypeDeclaration{
			Comment: schemaGodoc(name, spec),
			Type:    "int64",
			Name:    name,
			Schema:  spec,
		})
	case spec.Type.Is("number"):
		types = append(types, &TypeDeclaration{
			Comment: schemaGodoc(name, spec),
			Type:    "float64",
			Name:    name,
			Schema:  spec,
		})
	case spec.Type.Is("boolean"):
		types = append(types, &TypeDeclaration{
			Comment: schemaGodoc(name, spec),
			Type:    "bool",
			Name:    name,
			Schema:  spec,
		})
	case spec.Type.Is("array"):
		typeName, itemTypes := genSchema(spec.Items, stringx.MakeSingular(name))
		types = append(types, itemTypes...)
		types = append(types, &TypeDeclaration{
			Comment: schemaGodoc(name, spec),
			Type:    fmt.Sprintf("[]%s", typeName),
			Name:    name,
			Schema:  spec,
		})
	case spec.Type.Is("object"):
		object, additionalTypes := createObject(spec, name)
		types = append(types, object)
		types = append(types, additionalTypes...)

		if isErr {
			types = append(types, errorImplementation{
				Typ: object,
			})
		}
	case spec.OneOf != nil:
		object := createOneOf(spec, name)
		types = append(types, object)
		if isErr {
			types = append(types, errorImplementation{
				Typ: object,
			})
		}
	case spec.AnyOf != nil:
		slog.Warn("AnyOf not supported, falling back to 'inteface{}'",
			slog.Any("name", name),
		)
		types = append(types, &TypeDeclaration{
			Comment: schemaGodoc(name, spec),
			Type:    "interface{}",
			Name:    name,
			Schema:  spec,
		})
	case spec.AllOf != nil:
		object, additionalTypes := createAllOf(spec, name)
		types = append(types, object)
		types = append(types, additionalTypes...)
		if isErr {
			types = append(types, errorImplementation{
				Typ: object,
			})
		}
	default:
		slog.Warn("skipping unknown type",
			slog.Any("name", name),
			slog.Any("type", spec.Type),
		)
	}

	return types
}

// genSchema is very similar to [generateSchemaComponents] but assumes that all schema components
// have been already generated.
func genSchema(schema *openapi3.SchemaRef, name string) (string, []Writable) {
	if schema.Ref != "" {
		ref := strings.TrimPrefix(schema.Ref, "#/components/schemas/")
		if len(schema.Value.Enum) > 0 {
			return strcase.ToCamel(stringx.MakeSingular(ref)), nil
		}

		return strcase.ToCamel(ref), nil
	}

	types := make([]Writable, 0)
	spec := schema.Value

	switch {
	case len(spec.Enum) > 0:
		enum := createEnum(spec, name)
		if enum != nil {
			types = append(types, enum)
		}
		return stringx.MakeSingular(name), types
	case spec.Type.Is("string"):
		return formatStringType(schema.Value), nil
	case spec.Type.Is("integer"):
		return "int", nil
	case spec.Type.Is("number"):
		return "float64", nil
	case spec.Type.Is("boolean"):
		return "bool", nil
	case spec.Type.Is("array"):
		typeName, schemas := genSchema(spec.Items, stringx.MakeSingular(name))
		types = append(types, schemas...)
		return "[]" + typeName, types
	case spec.Type.Is("object"):
		object, additionalTypes := createObject(spec, name)
		types = append(types, object)
		types = append(types, additionalTypes...)
		return name, types
	case spec.OneOf != nil:
		object := createOneOf(spec, name)
		types = append(types, object)
		return name, types
	case spec.AnyOf != nil:
		slog.Warn("AnyOf not supported, falling back to 'any'",
			slog.Any("name", name),
		)
		return "interface{}", nil
	case spec.AllOf != nil:
		object, additionalTypes := createAllOf(spec, name)
		types = append(types, object)
		types = append(types, additionalTypes...)
		return name, types
	default:
		slog.Warn("skipping unknown type",
			slog.Any("name", name),
			slog.Any("type", spec.Type),
		)
		return "interface{}", nil
	}
}

// createObject converts openapi schema into golang object.
func createObject(schema *openapi3.Schema, name string) (*TypeDeclaration, []Writable) {
	if len(schema.Properties) == 0 &&
		(schema.AdditionalProperties.Has != nil && *schema.AdditionalProperties.Has) ||
		(schema.AdditionalProperties.Schema != nil) {
		return &TypeDeclaration{
			Comment: schemaGodoc(name, schema),
			Name:    name,
			Type:    "map[string]any",
			Schema:  schema,
		}, nil
	}

	fields, additionalTypes := createFields(schema.Properties, name, schema.Required)
	return &TypeDeclaration{
		Comment: schemaGodoc(name, schema),
		Name:    name,
		Type:    "struct",
		Fields:  fields,
		Schema:  schema,
	}, additionalTypes
}

// createFields returns list of fields for openapi schema properties.
func createFields(properties map[string]*openapi3.SchemaRef, name string, required []string) ([]StructField, []Writable) {
	fields := []StructField{}
	types := []Writable{}

	keys := slices.Collect(maps.Keys(properties))
	slices.Sort(keys)

	for _, property := range keys {
		schema := properties[property]
		typeName, moreTypes := genSchema(schema, name+strcase.ToCamel(property))
		tags := []string{strcase.ToSnake(property)}
		if !slices.Contains(required, property) {
			tags = append(tags, "omitempty")
		}
		optional := !slices.Contains(required, property)
		fields = append(fields, StructField{
			Name:    property,
			Type:    typeName,
			Comment: schemaPropertyGodoc(schema.Value),
			Tags: map[string][]string{
				"json": tags,
			},
			Optional: optional,
		})
		types = append(types, moreTypes...)
	}

	return fields, types
}

func createEnum(schema *openapi3.Schema, name string) Writable {
	enumName := stringx.MakeSingular(name)
	switch {
	case schema.Type.Is("string"):
		values := make([]EnumOption[string], 0)
		for _, v := range schema.Enum {
			option, ok := v.(string)
			if !ok {
				slog.Warn("invalid enum value",
					slog.String("enum", name),
					slog.String("expected", "string"),
					slog.String("got", fmt.Sprintf("%T", v)),
				)
				continue
			}

			values = append(values, EnumOption[string]{
				Name:  enumName + strcase.ToCamel(option),
				Value: option,
			})
		}

		return &EnumDeclaration[string]{
			Type: TypeDeclaration{
				Comment: schemaGodoc(name, schema),
				Name:    enumName,
				Type:    "string",
				Schema:  schema,
			},
			Values: values,
		}
	case schema.Type.Is("integer"):
		if schema.Format == "int64" {
			values := make([]EnumOption[int64], 0)
			for _, v := range schema.Enum {
				option, ok := v.(float64)
				if !ok {
					slog.Warn("invalid enum value",
						slog.String("enum", name),
						slog.String("expected", "int64"),
						slog.String("got", fmt.Sprintf("%T", v)),
					)
					continue
				}

				values = append(values, EnumOption[int64]{
					Name:  enumName + strcase.ToCamel(fmt.Sprintf("%v", option)),
					Value: int64(option),
				})
			}

			return &EnumDeclaration[int64]{
				Type: TypeDeclaration{
					Comment: schemaGodoc(name, schema),
					Name:    stringx.MakeSingular(name),
					Type:    "int64",
					Schema:  schema,
				},
				Values: values,
			}
		}

		values := make([]EnumOption[int], 0)
		for _, v := range schema.Enum {
			option, ok := v.(float64)
			if !ok {
				slog.Warn("invalid enum value",
					slog.String("enum", name),
					slog.String("expected", "int"),
					slog.String("got", fmt.Sprintf("%T", v)),
				)
				continue
			}

			values = append(values, EnumOption[int]{
				Name:  enumName + strcase.ToCamel(fmt.Sprintf("%v", option)),
				Value: int(option),
			})
		}

		return &EnumDeclaration[int]{
			Type: TypeDeclaration{
				Comment: schemaGodoc(name, schema),
				Name:    stringx.MakeSingular(name),
				Type:    "int",
				Schema:  schema,
			},
			Values: values,
		}
	case schema.Type.Is("number"):
		values := make([]EnumOption[float64], 0)
		for _, v := range schema.Enum {
			option, ok := v.(float64)
			if !ok {
				slog.Warn("invalid enum value",
					slog.String("enum", name),
					slog.String("expected", "float64"),
					slog.String("got", fmt.Sprintf("%T", v)),
				)
				continue
			}

			values = append(values, EnumOption[float64]{
				Name:  enumName + strcase.ToCamel(fmt.Sprintf("%v", option)),
				Value: option,
			})
		}

		return &EnumDeclaration[float64]{
			Type: TypeDeclaration{
				Comment: schemaGodoc(name, schema),
				Name:    stringx.MakeSingular(name),
				Type:    "float64",
				Schema:  schema,
			},
			Values: values,
		}
	default:
		return nil
	}
}

// createAllOf creates a type declaration for `allOf` schema.
func createAllOf(schema *openapi3.Schema, name string) (*TypeDeclaration, []Writable) {
	types := []Writable{}
	var fields []StructField
	var seen []string
	for _, s := range schema.AllOf {
		// Solve collision between the properties of `allOf` before we pass it further to avoid
		// generating nested objects and enums multiple times.
		properties := s.Value.Properties
		for _, f := range seen {
			delete(properties, f)
		}

		objectFields, additionalTypes := createFields(properties, name, s.Value.Required)
		fields = append(fields, objectFields...)
		types = append(types, additionalTypes...)

		seen = append(seen, slices.Collect(maps.Keys(properties))...)
	}

	return &TypeDeclaration{
		Comment: schemaGodoc(name, schema),
		Name:    name,
		Type:    "struct",
		Fields:  uniqueFields(fields),
		Schema:  schema,
	}, types
}

// createOneOf creates a type declaration for `oneOf` schema.
func createOneOf(schema *openapi3.Schema, name string) *TypeDeclaration {
	// TODO: implement `func (v *{{name}}) AsXXX() (XXX, error) { ... }`
	// that allows converting one of from `json.RawMessage` to possible variants.

	return &TypeDeclaration{
		Comment: schemaGodoc(name, schema),
		Name:    name,
		Type:    "json.RawMessage",
		Schema:  schema,
	}
}

func uniqueFields(fields []StructField) []StructField {
	return uniqueFunc(fields, func(f StructField) string { return f.Name })
}

func uniqueFunc[T any, C comparable](arr []T, keyFn func(T) C) []T {
	seen := make(map[C]bool)

	n := 0
	for _, ele := range arr {
		key := keyFn(ele)
		if ok := seen[key]; ok {
			continue
		}
		arr[n] = ele
		n++
		seen[key] = true
	}

	return arr[:n]
}

func getResponseName(operationName, responseCode string, content *openapi3.MediaType) string {
	if content.Schema.Value.Title != "" {
		return operationName + strcase.ToCamel(content.Schema.Value.Title) + "Response"
	}

	return operationName + responseCode + "Response"
}
