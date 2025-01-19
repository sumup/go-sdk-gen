package builder

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/iancoleman/strcase"
)

type templateData struct {
	PackageName string
	Types       []Writable
	Service     string
	Methods     []*Method
}

func (b *Builder) generateTagFile(tagName string, paths *openapi3.Paths) error {
	if tagName == "" {
		return fmt.Errorf("empty tag name")
	}

	resolvedSchemas := b.resolvedSchemas[tagName]
	resolvedResponses := b.resolvedResponses[tagName]
	if len(resolvedSchemas) == 0 && len(resolvedResponses) == 0 {
		return nil
	}

	tag := b.tagByTagName(tagName)

	types := b.schemasToTypes(resolvedSchemas, b.errorSchemas)

	bodyTypes := b.pathsToBodyTypes(paths)
	types = append(types, bodyTypes...)

	paramTypes := b.pathsToParamTypes(paths)
	types = append(types, paramTypes...)

	responseTypes := b.pathsToResponseTypes(paths)
	types = append(types, responseTypes...)

	respTypes := b.respToTypes(resolvedResponses, b.errorSchemas)
	types = append(types, respTypes...)

	methods, err := b.pathsToMethods(paths)
	if err != nil {
		return fmt.Errorf("convert paths to methods: %w", err)
	}

	slog.Info("generating file",
		slog.String("tag", tag.Name),
		slog.Int("schema_structs", len(types)),
		slog.Int("body_structs", len(bodyTypes)),
		slog.Int("path_params_structs", len(paramTypes)),
		slog.Int("response_structs", len(respTypes)),
	)

	fName := path.Join(b.cfg.Out, fmt.Sprintf("%s.go", strcase.ToSnake(tag.Name)))
	f, err := openGeneratedFile(fName)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := b.writeTagFile(f, templateData{
		PackageName: b.cfg.Pkg,
		Types:       types,
		Service:     strcase.ToCamel(tag.Name) + "Service",
		Methods:     methods,
	}); err != nil {
		return err
	}

	return nil
}

func (b *Builder) writeTagFile(f *os.File, config templateData) error {
	if err := b.templates.ExecuteTemplate(f, "tag.go.tmpl", config); err != nil {
		return err
	}

	return nil
}

func (b *Builder) writeClientFile(fname string, tags []string) error {
	f, err := os.OpenFile(fname, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.FileMode(0o755))
	if err != nil {
		return fmt.Errorf("create %q: %w", fname, err)
	}
	defer f.Close()

	for i := range tags {
		tags[i] = strcase.ToCamel(tags[i])
	}

	slices.Sort(tags)

	if err := b.templates.ExecuteTemplate(f, "client.go.tmpl", map[string]any{
		"PackageName": b.cfg.Pkg,
		"Tags":        tags,
		"Version":     b.spec.Info.Version,
	}); err != nil {
		return fmt.Errorf("generate client: %w", err)
	}

	return nil
}

func (b *Builder) writeTypesFile(fname string) error {
	f, err := os.OpenFile(fname, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.FileMode(0o755))
	if err != nil {
		return fmt.Errorf("create %q: %w", fname, err)
	}
	defer f.Close()

	if err := b.templates.ExecuteTemplate(f, "types.go.tmpl", map[string]any{
		"PackageName": b.cfg.Pkg,
	}); err != nil {
		return fmt.Errorf("generate client: %w", err)
	}

	return nil
}

func openGeneratedFile(filename string) (*os.File, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current working directory: %w", err)
	}

	p := filepath.Join(cwd, filename)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.FileMode(0o755))
	if err != nil {
		return nil, fmt.Errorf("create %q: %w", p, err)
	}

	return f, nil
}
