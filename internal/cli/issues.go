package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"

	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflowcontract"
	"github.com/liiujinfu/forgelane/internal/workitems"
	"github.com/spf13/cobra"
)

func newIssueCommand(stdout io.Writer, options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "List and start provider issues.",
	}
	cmd.AddCommand(newIssueListCommand(stdout, options))
	cmd.AddCommand(newIssueStartCommand(stdout, options))
	return cmd
}

func newIssueListCommand(stdout io.Writer, options Options) *cobra.Command {
	var ready bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List provider issues.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !ready {
				return fmt.Errorf("issue list currently requires --ready")
			}
			instanceStore, err := openReadOnlyStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			forgeProject, err := currentForgeProject(instanceStore)
			if err != nil {
				return err
			}
			repository := providerRepositoryFromForgeProject(forgeProject)
			labels, err := readyIssueLabels()
			if err != nil {
				return err
			}
			lister, err := workItemIssueListerForRepository(options, repository)
			if err != nil {
				return err
			}
			issues, err := lister.ListIssues(cmd.Context(), workitems.ProviderIssueListInput{
				Repository: repository,
				Labels:     labels,
			})
			if err != nil {
				return err
			}
			printReadyIssues(stdout, repository, issues)
			return nil
		},
	}
	cmd.Flags().BoolVar(&ready, "ready", false, "List issues marked ready for an agent")
	return cmd
}

func newIssueStartCommand(stdout io.Writer, options Options) *cobra.Command {
	var agentPreset string
	cmd := &cobra.Command{
		Use:   "start <provider-ref-or-issue>",
		Short: "Start an AgentRun from a provider issue.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return startAgentRunFromWorkItem(cmd, stdout, options, args[0], agentPreset, refreshSelectedWorkItemSnapshot)
		},
	}
	cmd.Flags().StringVar(&agentPreset, "agent-preset", "", "AgentAdapter preset for the RunSpec")
	return cmd
}

func providerRepositoryFromForgeProject(forgeProject store.ForgeProject) workitems.ProviderRepositoryRef {
	return workitems.ProviderRepositoryRef{
		Provider:       forgeProject.Provider,
		ProviderHost:   forgeProject.ProviderHost,
		RepositoryPath: forgeProject.RepositoryPath,
	}
}

func readyIssueLabels() ([]string, error) {
	root, err := workflowcontract.RepositoryRoot("")
	if err != nil {
		if errors.Is(err, workflowcontract.ErrRepositoryRootNotFound) {
			return workflowcontract.ReadyForAgentLabels("")
		}
		return nil, err
	}
	return workflowcontract.ReadyForAgentLabels(root)
}

func printReadyIssues(stdout io.Writer, repository workitems.ProviderRepositoryRef, issues []workitems.ProviderIssue) {
	sort.Slice(issues, func(i, j int) bool {
		return issues[i].ProviderIssueNumber < issues[j].ProviderIssueNumber
	})
	if len(issues) == 0 {
		fmt.Fprintf(stdout, "No ready issues for %s\n", repository.String())
		return
	}
	fmt.Fprintf(stdout, "Ready issues for %s\n", repository.String())
	for _, issue := range issues {
		fmt.Fprintf(stdout, "#%d %s\n", issue.ProviderIssueNumber, issue.Title)
	}
}
