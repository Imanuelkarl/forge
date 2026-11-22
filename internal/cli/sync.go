package cli

import (
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var noSwagger bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Re-read forge.yaml and regenerate every module, the wiring and the docs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := openProject(noSwagger)
			if err != nil {
				return err
			}
			if err := proj.gen.All(); err != nil {
				return err
			}
			if err := proj.writer.Finish(); err != nil {
				return err
			}
			return proj.gen.Swag()
		},
	}
	cmd.Flags().BoolVar(&noSwagger, "no-swagger", false, "do not generate Swagger annotations for this run")
	return cmd
}
