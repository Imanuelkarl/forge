package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Imanuelkarl/forge/internal/dialect"
	"github.com/Imanuelkarl/forge/internal/gen"
	"github.com/Imanuelkarl/forge/internal/naming"
	"github.com/Imanuelkarl/forge/internal/prompt"
	"github.com/Imanuelkarl/forge/internal/schema"
)

func newInitCmd() *cobra.Command {
	var (
		name, modulePath, db string
		idType               string
		noSwagger, skipTidy  bool
		yes                  bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up goforge inside an existing project (current directory) without touching your files",
		Long: `Set up goforge inside the project in the current directory.

init writes forge.yaml, .forge/ and whichever skeleton files are missing. It never
overwrites or edits a file that is already there, and it does not add a Go file that
would clash with code in its package (different package name, or a name that is
already declared). Everything it skipped is listed at the end.

The module path is read from go.mod and the database is guessed from the SQL driver
it requires. Afterwards use goforge as usual: goforge g m <name>, goforge sync, ...

Running init again in a goforge project fills in skeleton files that are missing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			p := prompt.New(yes)

			hadGoMod := gen.ModulePath(root) != ""
			s, resumed, err := initSchema(root, p, name, modulePath, db, idType, noSwagger)
			if err != nil {
				return err
			}

			w, err := gen.NewWriter(root, flagDryRun, flagForce, os.Stdout)
			if err != nil {
				return err
			}
			g, err := gen.New(root, s, w, os.Stdout)
			if err != nil {
				return err
			}

			if resumed {
				fmt.Printf("\n%s found: adding the skeleton files that are missing\n", schema.FileName)
			} else {
				fmt.Printf("\nAdding goforge to %s (%s, %s)\n", s.Project.Name, s.Project.Module, s.Project.Database)
			}
			if err := g.Project(); err != nil {
				return err
			}

			tidied := false
			if !flagDryRun && !skipTidy {
				run := true
				if hadGoMod {
					run = p.Confirm("Run `go mod tidy` to add goforge's dependencies to go.mod and go.sum?", true)
				}
				if run {
					if err := g.Tidy(); err != nil {
						fmt.Printf("go mod tidy failed (%v); fix the error and run it yourself\n", err)
					} else {
						tidied = true
						if err := g.UpdateGoVersion(gen.GoVersion(root)); err != nil {
							return err
						}
					}
				}
			}
			if err := w.Finish(); err != nil {
				return err
			}

			printInitNext(w, tidied || skipTidy || flagDryRun)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "project name (default: the directory name)")
	cmd.Flags().StringVar(&modulePath, "module", "", "Go module path (default: read from go.mod)")
	cmd.Flags().StringVar(&db, "db", "", "database: postgres | mysql (default: detected from go.mod)")
	cmd.Flags().StringVar(&idType, "id-type", "", "primary key type for every module: int | bigint | uuid (default int)")
	cmd.Flags().BoolVar(&noSwagger, "no-swagger", false, "do not generate Swagger annotations")
	cmd.Flags().BoolVar(&skipTidy, "skip-tidy", false, "do not run go mod tidy")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept defaults for every prompt")
	return cmd
}

// initSchema loads forge.yaml when the directory is already a goforge project (resumed),
// or builds the project settings from go.mod, flags and prompts.
func initSchema(root string, p *prompt.Prompter, name, modulePath, db, idType string, noSwagger bool) (*schema.Schema, bool, error) {
	path := filepath.Join(root, schema.FileName)
	if _, err := os.Stat(path); err == nil {
		s, err := schema.Load(path)
		if err != nil {
			return nil, false, err
		}
		return s, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	if parent, err := schema.FindRoot(filepath.Dir(root)); err == nil {
		return nil, false, fmt.Errorf("%s is inside the goforge project at %s; run goforge there instead", root, parent)
	}

	if name == "" {
		name = p.Input("Project name", naming.Kebab(filepath.Base(root)))
	}
	name = naming.Kebab(name)
	if name == "" {
		return nil, false, errors.New("project name must contain letters or digits")
	}

	if existing := gen.ModulePath(root); existing != "" {
		if modulePath != "" && modulePath != existing {
			return nil, false, fmt.Errorf("--module %s does not match go.mod (module %s)", modulePath, existing)
		}
		modulePath = existing
		fmt.Printf("Go module: %s (from go.mod)\n", modulePath)
	} else if modulePath == "" {
		modulePath = p.Input("Go module path (no go.mod found, one will be created)", "github.com/your-org/"+name)
	}

	if db == "" {
		def := 0
		if detected := gen.DetectDatabase(root); detected != "" {
			for i, n := range dialect.Names {
				if n == detected {
					def = i
				}
			}
			fmt.Printf("Database driver found in go.mod: %s\n", detected)
		}
		db = p.Select("Database", dialect.Names, def)
	}
	if idType == "" {
		idType = p.Select("Primary key type (int and bigint auto-increment)", schema.IDTypes, 0)
	}
	swagger := !noSwagger
	if !noSwagger {
		swagger = p.Confirm("Generate Swagger docs on handlers?", true)
	}

	s := &schema.Schema{
		Version: 1,
		Project: schema.Project{Name: name, Module: modulePath, Layout: "layered", Database: db, Swagger: swagger, IDType: idType},
	}
	s.Normalize()
	if err := s.Validate(); err != nil {
		return nil, false, err
	}
	return s, false, nil
}

func printInitNext(w *gen.Writer, depsReady bool) {
	kept := map[string]bool{}
	for _, k := range w.Kept {
		kept[k] = true
	}

	msg := "Done. goforge is set up; your existing files were not modified."
	if !depsReady {
		msg += "\nRun `go mod tidy` before building so go.mod gets goforge's dependencies."
	}
	if kept["cmd/server/main.go"] || kept["internal/handlers/router.go"] {
		msg += `

You already have your own server/router, so goforge's modules are not mounted yet.
Wire them in once, wherever you build your chi router:

    h := app.NewHandler(app.Deps{DB: db, Logger: logger, Validate: validator.New()})
    r.Route("/api/v1", h.Routes)      // every module goforge generates
    app.RegisterGlobalMiddleware(r)   // optional: middleware from forge.yaml

(imports: <module>/internal/app, github.com/go-playground/validator/v10)`
	}
	msg += `

Next:
  git status                      # review exactly what was added
  goforge generate module <name>  # add your first module
  goforge doctor                  # build, vet and test everything`
	printDone(msg)
}
