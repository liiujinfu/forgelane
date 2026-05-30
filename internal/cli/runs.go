package cli

import (
	"errors"
	"fmt"
	"io"

	githubprovider "github.com/liiujinfu/forgelane/internal/provider/github"
	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workitems"
	"github.com/spf13/cobra"
)

func newRunsCommand(stdout io.Writer, provider workitems.Provider) *cobra.Command {
	if provider == nil {
		provider = githubprovider.NewIssueProvider(githubprovider.Options{})
	}
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Create and inspect AgentRuns.",
	}
	cmd.AddCommand(newRunsCreateCommand(stdout, provider))
	return cmd
}

func newRunsCreateCommand(stdout io.Writer, provider workitems.Provider) *cobra.Command {
	return &cobra.Command{
		Use:   "create <provider-ref-or-issue>",
		Short: "Create a planned AgentRun and immutable RunSpec.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			ref, err := resolveWorkItemRef(args[0], instanceStore)
			if err != nil {
				return err
			}

			workItem, err := getOrImportWorkItem(cmd, instanceStore, provider, ref)
			if err != nil {
				return err
			}

			result, err := instanceStore.CreateAgentRun(workItem)
			if err != nil {
				return err
			}

			printCreatedAgentRun(stdout, workItem, result)
			return nil
		},
	}
}

func getOrImportWorkItem(cmd *cobra.Command, instanceStore *store.Store, provider workitems.Provider, ref workitems.ProviderRef) (store.WorkItem, error) {
	workItem, err := instanceStore.GetWorkItemByProviderRef(ref.String())
	if err == nil {
		return workItem, nil
	}
	if !errors.Is(err, store.ErrWorkItemNotFound) {
		return store.WorkItem{}, err
	}

	issue, err := provider.GetIssue(cmd.Context(), ref)
	if err != nil {
		return store.WorkItem{}, err
	}
	issue = issue.Normalize(ref)

	result, err := instanceStore.ImportWorkItem(issue)
	if err != nil {
		return store.WorkItem{}, err
	}
	return result.WorkItem, nil
}

func printCreatedAgentRun(stdout io.Writer, workItem store.WorkItem, result store.AgentRunCreateResult) {
	fmt.Fprintf(stdout, "Created AgentRun %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "WorkItem: %s\n", workItem.ProviderRef)
	fmt.Fprintf(stdout, "Status: %s\n", result.AgentRun.Status)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	fmt.Fprintf(stdout, "RunSpec ID: %d\n", result.RunSpec.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", result.Branch)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}
