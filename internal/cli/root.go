// Package cli wires goforge's commands.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/Imanuelkarl/forge/internal/gen"
	"github.com/Imanuelkarl/forge/internal/schema"
)

// version is overridden at build time with -ldflags "-X .../internal/cli.version=v1.2.3".
// When installed with `go install ...@vX.Y.Z` the module version is used instead.
var version = "0.2.0-dev"

func resolvedVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

var (
	flagDryRun bool
	flagForce  bool
)

// Execute runs the root command.
func Execute() error {
	return newRoot().Execute()
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "goforge",
		Short:         "Scaffold and evolve Go (Chi) backends that follow the MogRex engineering standard",
		Version:       resolvedVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "print what would be written without touching any file")
	root.PersistentFlags().BoolVar(&flagForce, "force", false, "overwrite scaffold files even if you edited them")

	root.AddCommand(newNewCmd(), newInitCmd(), newGenerateCmd(), newSyncCmd(), newMigrateCmd(), newDoctorCmd())
	return root
}

// project bundles everything a command needs once it is inside a goforge project.
type project struct {
	root   string
	schema *schema.Schema
	writer *gen.Writer
	gen    *gen.Generator
}

// openProject locates forge.yaml upward from the working directory and loads it.
func openProject(noSwagger bool) (*project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := schema.FindRoot(cwd)
	if err != nil {
		return nil, err
	}
	s, err := schema.Load(filepath.Join(root, schema.FileName))
	if err != nil {
		return nil, err
	}
	w, err := gen.NewWriter(root, flagDryRun, flagForce, os.Stdout)
	if err != nil {
		return nil, err
	}
	g, err := gen.New(root, s, w, os.Stdout)
	if err != nil {
		return nil, err
	}
	g.NoSwagger = noSwagger
	return &project{root: root, schema: s, writer: w, gen: g}, nil
}

func printDone(msg string) {
	fmt.Println("\n" + msg)
}
