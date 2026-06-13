// Package cli contains ForgeLane's command-line edge adapter.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
	"github.com/liiujinfu/forgelane/internal/version"
	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/liiujinfu/forgelane/internal/workflowcontract"
	"github.com/liiujinfu/forgelane/internal/workitems"
	"github.com/spf13/cobra"
)

// Options configures the root command's process edges.
type Options struct {
	Stdout                        io.Writer
	Stderr                        io.Writer
	WorkItemProvider              workitems.Provider
	WorkItemProviderFactory       func(workitems.ProviderRef) (workitems.Provider, error)
	AgentCommandPlanner           workflow.AgentCommandPlanner
	AgentCommandRunner            workflow.AgentCommandRunner
	RepositoryChangeMaterializer  workflow.RepositoryChangeMaterializer
	ChangeProvider                workflow.ChangeProvider
	ChangeProviderFactory         func(string) (workflow.ChangeProvider, error)
	ChangeFeedbackProvider        workflow.ChangeFeedbackProvider
	ChangeFeedbackProviderFactory func(string) (workflow.ChangeFeedbackProvider, error)
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
	root.AddCommand(newWorkflowCommand(stdout))
	root.AddCommand(newVersionCommand(stdout))
	root.AddCommand(newIssueCommand(stdout, options))
	root.AddCommand(newWorkItemsCommand(stdout, options))
	root.AddCommand(newRunsCommand(stdout, options))
	root.AddCommand(newPRCommand(stdout, options))
	root.AddCommand(newEventsCommand(stdout))

	return root
}

func newInitCommand(stdout io.Writer) *cobra.Command {
	var options repositoryconfig.InitOptions
	var withWorkflow bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure the local ForgeLane repository context.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			forgeProject, err := repositoryconfig.Configure(options)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(stdout, "Configured ForgeProject %s\n", repositoryconfig.ForgeProjectRef(forgeProject)); err != nil {
				return err
			}
			if withWorkflow {
				root, err := workflowcontract.RepositoryRoot("")
				if err != nil {
					return err
				}
				return createDefaultWorkflowContract(stdout, root)
			}
			root, err := workflowcontract.RepositoryRoot("")
			if err == nil {
				workflowExists, err := workflowcontract.Exists(root)
				if err != nil {
					return err
				}
				if !workflowExists {
					_, err = fmt.Fprintf(stdout, "Workflow contract missing; run `forgelane workflow init` or `forgelane init --with-workflow` to create %s\n", workflowcontract.FileName)
					return err
				}
			} else if !errors.Is(err, workflowcontract.ErrRepositoryRootNotFound) {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&options.RepoURL, "repo-url", "", "Provider repository URL")
	cmd.Flags().StringVar(&options.Provider, "provider", "", "WorkItem provider")
	cmd.Flags().StringVar(&options.Repo, "repo", "", "Provider repository path")
	cmd.Flags().BoolVar(&withWorkflow, "with-workflow", false, "Create the default repo-owned workflow contract")

	return cmd
}

func newWorkflowCommand(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage the repo-owned ForgeLane workflow contract.",
	}
	cmd.AddCommand(newWorkflowInitCommand(stdout))
	return cmd
}

func newWorkflowInitCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the default repo-owned workflow contract.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := workflowcontract.RepositoryRoot("")
			if err != nil {
				return err
			}
			return createDefaultWorkflowContract(stdout, root)
		},
	}
}

func createDefaultWorkflowContract(stdout io.Writer, repositoryRoot string) error {
	if err := workflowcontract.InitDefault(repositoryRoot); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "Created workflow contract %s\n", workflowcontract.FileName)
	return err
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
