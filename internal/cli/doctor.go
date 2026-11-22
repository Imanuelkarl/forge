package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Imanuelkarl/forge/internal/schema"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the project: gofmt, go build, go vet, go test and golangci-lint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := schema.FindRoot(cwd)
			if err != nil {
				return err
			}

			failed := 0
			step := func(label string, name string, args ...string) {
				fmt.Printf("• %s ... ", label)
				c := exec.Command(name, args...)
				c.Dir = root
				out, err := c.CombinedOutput()
				text := strings.TrimSpace(string(out))
				if label == "gofmt" && err == nil && text != "" {
					err = errors.New("these files need formatting (run `make fmt`):\n" + text)
					text = ""
				}
				if err != nil {
					failed++
					fmt.Println("FAILED")
					if text != "" {
						fmt.Println(text)
					}
					if label == "gofmt" {
						fmt.Println(err)
					}
					return
				}
				fmt.Println("ok")
			}

			step("gofmt", "gofmt", "-l", ".")
			step("go build", "go", "build", "./...")
			step("go vet", "go", "vet", "./...")
			step("go test", "go", "test", "./...", "-count=1")

			if _, err := exec.LookPath("golangci-lint"); err == nil {
				step("golangci-lint", "golangci-lint", "run", "./...")
			} else {
				fmt.Println("• golangci-lint ... skipped (not installed)")
			}

			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			fmt.Println("\nAll checks passed.")
			return nil
		},
	}
}
