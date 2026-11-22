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

func newNewCmd() *cobra.Command {
	var (
		modulePath, db      string
		idType              string
		noSwagger, skipTidy bool
		yes                 bool
	)

	cmd := &cobra.Command{
		Use:     "new [name]",
		Aliases: []string{"init"},
		Short:   "Create a new project (skeleton, forge.yaml, .env, migrations runner)",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := prompt.New(yes)

			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				name = p.Input("Project name", "my-api")
			}
			name = naming.Kebab(name)
			if name == "" {
				return errors.New("project name must contain letters or digits")
			}

			if modulePath == "" {
				modulePath = p.Input("Go module path", "github.com/your-org/"+name)
			}
			if db == "" {
				db = p.Select("Database", dialect.Names, 0)
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
				return err
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root := filepath.Join(cwd, name)
			if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 && !flagForce {
				return fmt.Errorf("directory %s already exists and is not empty (use --force to scaffold into it)", name)
			}
			if !flagDryRun {
				if err := os.MkdirAll(root, 0o755); err != nil {
					return err
				}
			}

			w, err := gen.NewWriter(root, flagDryRun, flagForce, os.Stdout)
			if err != nil {
				return err
			}
			g, err := gen.New(root, s, w, os.Stdout)
			if err != nil {
				return err
			}

			fmt.Printf("\nCreating %s (%s, %s)\n", name, modulePath, db)
			if err := g.Project(); err != nil {
				return err
			}

			if !flagDryRun && !skipTidy {
				if err := g.Tidy(); err != nil {
					fmt.Printf("go mod tidy failed (%v); run it yourself once you have network access\n", err)
				} else if err := g.UpdateGoVersion(gen.GoVersion(root)); err != nil {
					return err
				}
			}
			if err := w.Finish(); err != nil {
				return err
			}

			printDone(fmt.Sprintf("Done. Next steps:\n  cd %s\n  # edit .env (DATABASE_URL), create the database, then:\n  goforge generate module <name>\n  goforge migrate up\n  make run", name))
			return nil
		},
	}

	cmd.Flags().StringVar(&modulePath, "module", "", "Go module path (e.g. github.com/acme/orders-api)")
	cmd.Flags().StringVar(&db, "db", "", "database: postgres | mysql")
	cmd.Flags().StringVar(&idType, "id-type", "", "primary key type for every module: int | bigint | uuid (default int)")
	cmd.Flags().BoolVar(&noSwagger, "no-swagger", false, "do not generate Swagger annotations")
	cmd.Flags().BoolVar(&skipTidy, "skip-tidy", false, "do not run go mod tidy")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept defaults for every prompt")
	return cmd
}
