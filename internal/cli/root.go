// Package cli contains ForgeLane's command-line edge adapter.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
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

	root.AddCommand(newInitCommand(stdout))
	root.AddCommand(newVersionCommand(stdout))

	return root
}

func newInitCommand(stdout io.Writer) *cobra.Command {
	var options repositoryconfig.InitOptions

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure the local ForgeLane repository context.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			forgeProject, err := repositoryconfig.Configure(options)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(stdout, "Configured ForgeProject %s\n", repositoryconfig.ForgeProjectRef(forgeProject))
			return err
		},
	}

	cmd.Flags().StringVar(&options.RepoURL, "repo-url", "", "GitHub repository URL")
	cmd.Flags().StringVar(&options.Provider, "provider", "", "WorkItem provider")
	cmd.Flags().StringVar(&options.Repo, "repo", "", "Provider repository path")

	return cmd
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
