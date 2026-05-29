// Package cli contains ForgeLane's command-line edge adapter.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/liiujinfu/forgelane/internal/version"
	"github.com/spf13/cobra"
)

// Options configures the root command's process edges.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
}

// NewRootCommand constructs the ForgeLane CLI command tree.
func NewRootCommand(options Options) *cobra.Command {
	stdout := options.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	root := &cobra.Command{
		Use:     "forgelane",
		Short:   "ForgeLane is an agentic software delivery control plane.",
		Version: version.Version,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Name}} version {{.Version}}\n")

	root.AddCommand(newVersionCommand(stdout))

	return root
}

func newVersionCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print ForgeLane version information.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(stdout, "Version: %s\nCommit: %s\nDate: %s\n", version.Version, version.Commit, version.Date)
			return err
		},
	}
}
