// Command goforge scaffolds and evolves Go backends that follow the MogRex Backend Engineering Standard.
package main

import (
	"fmt"
	"os"

	"github.com/Imanuelkarl/forge/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
