package builder

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/iancoleman/strcase"
)

type Writable interface {
	String() string
}

func (tt *TypeDeclaration) String() string {
	buf := new(strings.Builder)
	if tt.Comment != "" {
		fmt.Fprintf(buf, "// %s\n", tt.Comment)
	}
	fmt.Fprintf(buf, "type %s %s", tt.Name, tt.Type)
	if tt.Fields != nil {
		slices.SortFunc(tt.Fields, func(a, b StructField) int {
			return strings.Compare(a.Name, b.Name)
		})
		fmt.Fprint(buf, " {\n")
		for _, ft := range tt.Fields {
			fmt.Fprint(buf, ft.String())
			fmt.Fprint(buf, "\n")
		}
		fmt.Fprint(buf, "}")
	}
	fmt.Fprint(buf, "\n")
	return buf.String()
}

func (o *OneOfDeclaration) String() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "type %s struct {\n", o.Name)
	for _, opt := range o.Options {
		fmt.Fprintf(buf, "\t%s *%s\n", opt, opt)
	}
	fmt.Fprintf(buf, "}\n\n")
	for _, opt := range o.Options {
		fmt.Fprintf(buf, "func (r *%s) As%s() (*%s, bool) {\n", o.Name, opt, opt)
		fmt.Fprintf(buf, "\tif r.%s != nil {\n", opt)
		fmt.Fprintf(buf, "\t\treturn r.%s, true\n", opt)
		fmt.Fprint(buf, "\t}\n\n")
		fmt.Fprint(buf, "\t\treturn nil, false\n")
		fmt.Fprint(buf, "\t}\n\n")
	}
	return buf.String()
}

func (f *StructField) String() string {
	buf := new(strings.Builder)
	if f.Comment != "" {
		fmt.Fprintf(buf, "// %s\n", f.Comment)
	}
	name := f.Name

	// TODO: extract into helper
	if strings.HasPrefix(name, "+") {
		name = strings.Replace(name, "+", "Plus", 1)
	}
	if strings.HasPrefix(name, "-") {
		name = strings.Replace(name, "-", "Minus", 1)
	}
	if strings.HasPrefix(name, "@") {
		name = strings.Replace(name, "@", "At", 1)
	}
	if strings.HasPrefix(name, "$") {
		name = strings.Replace(name, "$", "", 1)
	}

	name = strcase.ToCamel(name)
	if f.Optional {
		fmt.Fprintf(buf, "\t%s *%s", name, f.Type)
	} else {
		fmt.Fprintf(buf, "\t%s %s", name, f.Type)
	}
	if len(f.Tags) > 0 {
		fmt.Fprint(buf, " `")
		for k, v := range f.Tags {
			fmt.Fprintf(buf, "%s:%q", k, strings.Join(v, ","))
		}
		fmt.Fprint(buf, "`")
	}

	return buf.String()
}

func (et *EnumDeclaration[E]) String() string {
	buf := new(strings.Builder)
	fmt.Fprint(buf, et.Type.String())
	fmt.Fprint(buf, "\nconst (\n")
	slices.SortFunc(et.Values, func(a, b EnumOption[E]) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, v := range et.Values {
		fmt.Fprintf(buf, "\t%s %s = %#v\n", v.Name, et.Type.Name, v.Value)
	}
	fmt.Fprint(buf, ")\n")
	return buf.String()
}

func paramToString(name string, param *openapi3.Parameter) string {
	// HACK:
	if param.Schema.Ref != "" {
		return fmt.Sprintf("string(%s)", name)
	}

	switch {
	case param.Schema.Value.Type.Is("string"):
		switch param.Schema.Value.Format {
		case "date-time":
			name = strings.TrimPrefix(name, "*")
			return fmt.Sprintf("%s.Format(time.RFC3339)", name)
		case "date":
			name = strings.TrimPrefix(name, "*")
			return fmt.Sprintf("%s.String()", name)
		case "time":
			name = strings.TrimPrefix(name, "*")
			return fmt.Sprintf("%s.String()", name)
		default:
			return name
		}
	case param.Schema.Value.Type.Is("integer"):
		return fmt.Sprintf("strconv.Itoa(%s)", name)
	case param.Schema.Value.Type.Is("boolean"):
		return fmt.Sprintf("strconv.FormatBool(%s)", name)
	case param.Schema.Value.Type.Is("number"):
		return fmt.Sprintf("strconv.FormatFloat(%s, 'f', -1, 64)", name)
	default:
		slog.Info("need to implement conversion for",
			slog.String("ref", param.Schema.Ref),
			slog.String("type", strings.Join(param.Schema.Value.Type.Slice(), ",")),
			slog.String("name", name),
		)
		return name
	}
}

type toQueryValues struct {
	Typ *TypeDeclaration
}

func (e toQueryValues) String() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "// QueryValues converts [%s] into [url.Values].\n", e.Typ.Name)
	fmt.Fprintf(buf, "func (p *%s) QueryValues() url.Values {\n", e.Typ.Name)
	fmt.Fprintf(buf, "\tq := make(url.Values)\n\n")
	for _, f := range e.Typ.Fields {
		name := strcase.ToCamel(f.Name)
		if f.Parameter.Schema.Value.Type.Is("array") {
			if f.Parameter.Required {
				field := fmt.Sprintf("p.%s", name)
				fmt.Fprintf(buf, "\tfor _, v := range %s {\n", field)
				fmt.Fprintf(buf, "\t\tq.Add(%q, %s)\n", f.Name, paramToString("v", f.Parameter))
				fmt.Fprintf(buf, "\t}\n")
			} else {
				fmt.Fprintf(buf, "\tif p.%s != nil {\n", name)

				field := fmt.Sprintf("*p.%s", name)
				fmt.Fprintf(buf, "\t\tfor _, v := range %s {\n", field)
				fmt.Fprintf(buf, "\t\t\tq.Add(%q, %s)\n", f.Name, paramToString("v", f.Parameter))
				fmt.Fprintf(buf, "\t\t}\n")

				fmt.Fprintf(buf, "\t}\n")
			}
		} else {
			if f.Parameter.Required {
				field := fmt.Sprintf("p.%s", name)
				fmt.Fprintf(buf, "\tq.Set(%q, %s)\n", f.Name, paramToString(field, f.Parameter))
			} else {
				fmt.Fprintf(buf, "\tif p.%s != nil {\n", name)
				field := fmt.Sprintf("*p.%s", name)
				fmt.Fprintf(buf, "\t\tq.Set(%q, %s)\n", f.Name, paramToString(field, f.Parameter))
				fmt.Fprintf(buf, "\t}\n")
			}
		}
		fmt.Fprint(buf, "\n")
	}
	fmt.Fprintf(buf, "\treturn q\n")
	fmt.Fprint(buf, "}\n")
	return buf.String()
}

type typeAssertionDeclaration struct {
	typ string
}

func (e typeAssertionDeclaration) String() string {
	return fmt.Sprintf(`var _ error = (*%s)(nil)`, e.typ)
}

// errorImplementation is used to generate `error` interface for types returned
// by error responses.
type errorImplementation struct {
	Typ *TypeDeclaration
}

func (e errorImplementation) String() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "func (e *%s) Error() string {\n", e.Typ.Name)
	fmt.Fprintf(buf, "\treturn fmt.Sprintf(\"")
	for i, f := range e.Typ.Fields {
		if i > 0 {
			fmt.Fprint(buf, ", ")
		}
		fmt.Fprintf(buf, "%s=%%v", f.Name)
	}
	fmt.Fprint(buf, "\", ")
	for i, f := range e.Typ.Fields {
		if i > 0 {
			fmt.Fprint(buf, ", ")
		}
		fmt.Fprintf(buf, "e.%s", strcase.ToCamel(f.Name))
	}
	fmt.Fprint(buf, ")\n")
	fmt.Fprint(buf, "}\n")
	return buf.String()
}

// staticErrorImplementation implements `error` for responses with empty schemas.
type staticErrorImplementation struct {
	Typ  string
	Name string
}

func (e staticErrorImplementation) String() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "func (e *%s) Error() string {\n", e.Typ)
	fmt.Fprintf(buf, "\treturn %q\n", e.Name)
	fmt.Fprint(buf, "}\n")
	return buf.String()
}
