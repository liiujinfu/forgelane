package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	githubprovider "github.com/liiujinfu/forgelane/internal/provider/github"
	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
	"github.com/liiujinfu/forgelane/internal/runner"
	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflow"
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
	cmd.AddCommand(newRunsShowCommand(stdout))
	cmd.AddCommand(newRunsPrepareCommand(stdout))
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

			result, err := workflow.CreatePlannedAgentRun(instanceStore, workflow.CreatePlannedAgentRunInput{
				WorkItem: workItem,
			})
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

func printCreatedAgentRun(stdout io.Writer, workItem store.WorkItem, result workflow.AgentRunCreateResult) {
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

func newRunsPrepareCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "prepare <run_id>",
		Short: "Lease a Workspace and prepare its repository checkout.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			runID, err := parseAgentRunID(args[0])
			if err != nil {
				return err
			}

			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			paths, err := workspacePathsForRun(runID)
			if err != nil {
				return err
			}
			result, err := workflow.PrepareAgentRunWorkspace(instanceStore, runner.LocalWorkspacePreparer{}, runID, paths)
			if err != nil {
				return err
			}
			printPreparedAgentRun(stdout, result)
			return nil
		},
	}
}

func workspacePathsForRun(runID int64) (store.WorkspacePaths, error) {
	dbPath, err := repositoryconfig.StateDBPath("")
	if err != nil {
		return store.WorkspacePaths{}, err
	}
	root := filepath.Join(filepath.Dir(dbPath), "workspaces", fmt.Sprintf("run-%d", runID))
	return store.WorkspacePaths{
		Root:      root,
		Repo:      filepath.Join(root, "repo"),
		Logs:      filepath.Join(root, "logs"),
		Artifacts: filepath.Join(root, "artifacts"),
		Tmp:       filepath.Join(root, "tmp"),
	}, nil
}

func printPreparedAgentRun(stdout io.Writer, result store.AgentRunPrepareResult) {
	fmt.Fprintf(stdout, "Prepared AgentRun %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "RunnerJob ID: %d\n", result.RunnerJob.ID)
	fmt.Fprintf(stdout, "Workspace ID: %d\n", result.Workspace.ID)
	fmt.Fprintf(stdout, "Workspace: %s\n", result.Workspace.Paths.Root)
	fmt.Fprintf(stdout, "Repository: %s\n", result.Workspace.Paths.Repo)
	fmt.Fprintf(stdout, "Status: %s\n", result.Workspace.Status)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func newRunsShowCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "show <run_id>",
		Short: "Show AgentRun detail from local state.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			runID, err := parseAgentRunID(args[0])
			if err != nil {
				return err
			}

			instanceStore, err := openReadOnlyStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			detail, err := instanceStore.GetAgentRunDetail(runID)
			if err != nil {
				return err
			}
			return printAgentRunDetail(stdout, detail)
		},
	}
}

func parseAgentRunID(input string) (int64, error) {
	runID, err := strconv.ParseInt(input, 10, 64)
	if err != nil || runID <= 0 {
		return 0, fmt.Errorf("invalid AgentRun id: %s", input)
	}
	return runID, nil
}

func printAgentRunDetail(stdout io.Writer, detail store.AgentRunDetail) error {
	var spec map[string]any
	if err := json.Unmarshal([]byte(detail.RunSpec.SpecJSON), &spec); err != nil {
		return fmt.Errorf("decode RunSpec %d: %w", detail.RunSpec.ID, err)
	}

	fmt.Fprintf(stdout, "AgentRun %d\n", detail.AgentRun.ID)
	fmt.Fprintf(stdout, "WorkItem: %s\n", detail.WorkItem.ProviderRef)
	fmt.Fprintf(stdout, "Status: %s\n", detail.AgentRun.Status)
	fmt.Fprintf(stdout, "Created: %s\n", detail.AgentRun.CreatedAt)
	fmt.Fprintf(stdout, "Updated: %s\n", detail.AgentRun.UpdatedAt)
	fmt.Fprintf(stdout, "RunSpec ID: %d\n", detail.RunSpec.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", stringField(spec, "branch"))
	fmt.Fprintf(stdout, "Repository: %s\n", nestedStringField(spec, "repo", "ref"))
	fmt.Fprintf(stdout, "AgentAdapter: %s\n", agentAdapterSummary(spec))
	if detail.Workspace != nil {
		fmt.Fprintf(stdout, "Workspace status: %s\n", detail.Workspace.Status)
		fmt.Fprintf(stdout, "Workspace: %s\n", detail.Workspace.Paths.Root)
		fmt.Fprintf(stdout, "Workspace repo: %s\n", detail.Workspace.Paths.Repo)
		fmt.Fprintf(stdout, "Workspace logs: %s\n", detail.Workspace.Paths.Logs)
		fmt.Fprintf(stdout, "Workspace artifacts: %s\n", detail.Workspace.Paths.Artifacts)
		fmt.Fprintf(stdout, "Workspace tmp: %s\n", detail.Workspace.Paths.Tmp)
	}
	return nil
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func nestedStringField(values map[string]any, parent string, key string) string {
	child, ok := values[parent].(map[string]any)
	if !ok {
		return ""
	}
	return stringField(child, key)
}

func agentAdapterSummary(spec map[string]any) string {
	agentAdapter, ok := spec["agent_adapter"].(map[string]any)
	if !ok {
		return ""
	}
	kind := stringField(agentAdapter, "kind")
	preset := stringField(agentAdapter, "preset")
	if preset == "" {
		return kind
	}
	return fmt.Sprintf("%s preset=%s", kind, preset)
}
