package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Imanuelkarl/forge/internal/gen"
	"github.com/Imanuelkarl/forge/internal/naming"
	"github.com/Imanuelkarl/forge/internal/prompt"
	"github.com/Imanuelkarl/forge/internal/schema"
)

func newGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "generate",
		Aliases: []string{"g"},
		Short:   "Generate a module or a middleware",
	}
	cmd.AddCommand(newGenModuleCmd(), newGenMiddlewareCmd())
	return cmd
}

func newGenModuleCmd() *cobra.Command {
	var (
		fieldSpecs []string
		noSwagger  bool
		yes        bool
	)

	cmd := &cobra.Command{
		Use:     "module [name]",
		Aliases: []string{"m"},
		Short:   "Generate a module (domain, handler, service, repositories, migration, tests)",
		Long: `Generate a module.

If the module already exists in forge.yaml it is generated from there.
Otherwise goforge asks for its fields (or reads --field flags), appends it to forge.yaml and generates it.

Examples:
  goforge g m order
  goforge g m product --yes --field name:string:required,max=100 --field price:decimal:required --field sku:string:unique`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := openProject(noSwagger)
			if err != nil {
				return err
			}
			p := prompt.New(yes)

			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				name = p.Input("Module name (singular, e.g. order)", "")
			}
			name = naming.Snake(name)
			if name == "" {
				return errors.New("module name is required")
			}

			if existing, _ := proj.schema.Module(name); existing == nil {
				if err := addModuleToSchema(proj, p, name, fieldSpecs); err != nil {
					return err
				}
			}

			if err := proj.gen.Module(name); err != nil {
				return err
			}
			if err := proj.gen.RefreshProjectFiles(); err != nil {
				return err
			}
			if err := proj.gen.Wiring(); err != nil {
				return err
			}
			if err := proj.writer.Finish(); err != nil {
				return err
			}
			if err := proj.gen.Swag(); err != nil {
				return err
			}

			printDone("Module generated. Next:\n  goforge migrate up      # create the table\n  make run")
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&fieldSpecs, "field", nil, "field as name:type[:opt,opt] (repeatable); options: required nullable unique index email url min=N max=N default=X values=a|b ref=table.col")
	cmd.Flags().BoolVar(&noSwagger, "no-swagger", false, "do not generate Swagger annotations for this run")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept defaults for every prompt")
	return cmd
}

// addModuleToSchema collects a new module's definition, validates it together with the
// existing modules, appends it to forge.yaml and reloads the schema.
func addModuleToSchema(proj *project, p *prompt.Prompter, name string, specs []string) error {
	m, err := collectModule(p, name, specs)
	if err != nil {
		return err
	}

	// Validate before touching the file.
	candidate := *proj.schema
	candidate.Modules = append(append(schema.Modules{}, proj.schema.Modules...), m)
	candidate.Normalize()
	if err := candidate.Validate(); err != nil {
		return fmt.Errorf("invalid module definition:\n%w", err)
	}

	path := filepath.Join(proj.root, schema.FileName)
	if flagDryRun {
		fmt.Printf("would append to %s:\n%s", schema.FileName, schema.RenderModule(m))
		proj.schema.Modules = candidate.Modules
		return nil
	}
	if err := schema.AppendModule(path, m); err != nil {
		if errors.Is(err, schema.ErrNotEditable) {
			return fmt.Errorf("%w\nadd this under `modules:` yourself and re-run:\n\n%s", err, schema.RenderModule(m))
		}
		return err
	}
	fmt.Printf("added %q to %s\n", name, schema.FileName)

	reloaded, err := schema.Load(path)
	if err != nil {
		return err
	}
	proj.schema.Modules = reloaded.Modules
	return nil
}

func collectModule(p *prompt.Prompter, name string, specs []string) (schema.Module, error) {
	m := schema.Module{Name: name}

	for _, spec := range specs {
		f, err := schema.ParseFieldSpec(spec)
		if err != nil {
			return m, err
		}
		m.Fields = append(m.Fields, f)
	}

	if len(m.Fields) == 0 {
		if !p.Interactive() {
			return m, errors.New("no fields given: pass --field name:type or run without --yes to be prompted")
		}
		fmt.Printf("Define the fields of %q (id, created_at and updated_at are added automatically).\n", name)
		for {
			fname := p.Input("Field name (blank to finish)", "")
			if fname == "" {
				break
			}
			f := schema.Field{Name: naming.Snake(fname)}
			f.Type = p.Select("Type", schema.TypeNames, 0)
			if f.Type == "enum" {
				for _, v := range strings.Split(p.Input("Allowed values (comma separated)", ""), ",") {
					if v = strings.TrimSpace(v); v != "" {
						f.Values = append(f.Values, v)
					}
				}
			}
			f.Required = p.Confirm("Required on create?", true)
			if !f.Required {
				f.Nullable = p.Confirm("Nullable (stored as NULL)?", false)
			}
			if schema.UniqueAllowed(f.Type) && f.Required {
				f.Unique = p.Confirm("Unique?", false)
			}
			if f.Type == "string" {
				if v := p.Input("Max length (blank = 255)", ""); v != "" {
					if n, err := strconv.Atoi(v); err == nil {
						f.Max = &n
					}
				}
			}
			m.Fields = append(m.Fields, f)
		}
	}

	if p.Interactive() && p.Confirm("Add custom endpoints?", false) {
		for {
			ename := p.Input("Endpoint name (blank to finish, e.g. cancel)", "")
			if ename == "" {
				break
			}
			method := p.Select("HTTP method", []string{"POST", "GET", "PUT", "PATCH", "DELETE"}, 0)
			path := p.Input("Path", "/{id}/"+naming.Kebab(ename))
			m.Endpoints.Custom = append(m.Endpoints.Custom, schema.CustomEndpoint{
				Name: naming.Snake(ename), Method: method, Path: path,
			})
		}
	}
	return m, nil
}

func newGenMiddlewareCmd() *cobra.Command {
	var (
		preset   string
		noGlobal bool
		yes      bool
	)

	cmd := &cobra.Command{
		Use:     "middleware [name]",
		Aliases: []string{"mw"},
		Short:   "Generate a middleware in internal/middleware and register it globally",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := openProject(false)
			if err != nil {
				return err
			}
			p := prompt.New(yes)

			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				name = p.Input("Middleware name (e.g. security-headers)", "")
			}
			if name == "" {
				return errors.New("middleware name is required")
			}
			if preset == "" {
				preset = p.Select("Template", gen.MiddlewarePresets, 0)
			}
			global := !noGlobal
			if !noGlobal {
				global = p.Confirm("Register globally on the root router?", true)
			}

			if err := proj.gen.Middleware(name, preset, global); err != nil {
				return err
			}
			if err := proj.writer.Finish(); err != nil {
				return err
			}
			if preset == "rate-limit" && !flagDryRun {
				if err := proj.gen.Tidy(); err != nil {
					fmt.Printf("go mod tidy failed (%v); run it yourself to fetch go-chi/httprate\n", err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&preset, "preset", "", "template: "+strings.Join(gen.MiddlewarePresets, " | "))
	cmd.Flags().BoolVar(&noGlobal, "no-global", false, "only create the file; do not register it on the root router")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept defaults for every prompt")
	return cmd
}
