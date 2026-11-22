package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Imanuelkarl/forge/internal/schema"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate <up|down|status|goto|force|create> [args]",
		Short: "Run database migrations (wraps your project's cmd/migrate)",
		Long: `Run database migrations.

This is a thin wrapper around "go run ./cmd/migrate" inside your project, so it behaves
exactly like "make migrate-up" and reads DATABASE_URL from .env.

  goforge migrate up            apply all pending migrations
  goforge migrate up 1          apply the next migration only
  goforge migrate down          roll back the last migration
  goforge migrate down 3        roll back three; "down all" rolls back everything
  goforge migrate status        show the current version
  goforge migrate goto <ver>    migrate to a specific version
  goforge migrate force <ver>   set the version after a failed (dirty) migration
  goforge migrate create <name> create an empty up/down pair`,
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := schema.FindRoot(cwd)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(root, "cmd", "migrate", "main.go")); err != nil {
				return errors.New("cmd/migrate/main.go not found in this project")
			}

			c := exec.Command("go", append([]string{"run", "./cmd/migrate"}, args...)...)
			c.Dir = root
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			if err := c.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					os.Exit(ee.ExitCode())
				}
				return fmt.Errorf("run migrate: %w", err)
			}
			return nil
		},
	}
}
