package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	commandadapter "github.com/liiujinfu/forgelane/internal/agentadapter/command"
	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
	"github.com/liiujinfu/forgelane/internal/runner"
	processrunner "github.com/liiujinfu/forgelane/internal/runner/process"
	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/liiujinfu/forgelane/internal/workflowcontract"
	"github.com/liiujinfu/forgelane/internal/workitems"
	"github.com/spf13/cobra"
)

func newRunsCommand(stdout io.Writer, options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Create and inspect AgentRuns.",
	}
	cmd.AddCommand(newRunsCreateCommand(stdout, options))
	cmd.AddCommand(newRunsStartCommand(stdout, options))
	cmd.AddCommand(newRunsShowCommand(stdout))
	cmd.AddCommand(newRunsEvidenceCommand(stdout))
	cmd.AddCommand(newRunsPrepareCommand(stdout))
	cmd.AddCommand(newRunsExecuteCommand(stdout, options))
	cmd.AddCommand(newRunsStopCommand(stdout))
	cmd.AddCommand(newRunsRequestChangesCommand(stdout))
	cmd.AddCommand(newRunsCloseCommand(stdout))
	cmd.AddCommand(newRunsRequestAttentionCommand(stdout))
	cmd.AddCommand(newRunsSendCommand(stdout))
	cmd.AddCommand(newRunsApproveCommand(stdout))
	cmd.AddCommand(newRunsRetryCommand(stdout))
	cmd.AddCommand(newRunsLogsCommand(stdout))
	cmd.AddCommand(newRunsReportCommand(stdout, options))
	return cmd
}

func newRunsCreateCommand(stdout io.Writer, options Options) *cobra.Command {
	var agentPreset string
	cmd := &cobra.Command{
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
			provider, err := workItemProviderForRef(options, ref)
			if err != nil {
				return err
			}

			workItem, err := getOrImportWorkItem(cmd, instanceStore, provider, ref)
			if err != nil {
				return err
			}
			selectedPreset, err := agentPresetForRun(agentPreset)
			if err != nil {
				return err
			}

			result, err := workflow.CreatePlannedAgentRun(instanceStore, workflow.CreatePlannedAgentRunInput{
				WorkItem:    workItem,
				AgentPreset: selectedPreset,
			})
			if err != nil {
				return err
			}

			printCreatedAgentRun(stdout, workItem, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentPreset, "agent-preset", "", "AgentAdapter preset for the RunSpec")
	return cmd
}

func newRunsStartCommand(stdout io.Writer, options Options) *cobra.Command {
	var agentPreset string
	cmd := &cobra.Command{
		Use:   "start <provider-ref-or-issue>",
		Short: "Start an AgentRun and deliver its draft PR path.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return startAgentRunFromWorkItem(cmd, stdout, options, args[0], agentPreset, useCachedWorkItemSnapshot)
		},
	}
	cmd.Flags().StringVar(&agentPreset, "agent-preset", "", "AgentAdapter preset for the RunSpec")
	return cmd
}

type workItemSnapshotMode int

const (
	useCachedWorkItemSnapshot workItemSnapshotMode = iota
	refreshSelectedWorkItemSnapshot
)

func startAgentRunFromWorkItem(cmd *cobra.Command, stdout io.Writer, options Options, input string, agentPreset string, snapshotMode workItemSnapshotMode) error {
	instanceStore, err := openInitializedStore()
	if err != nil {
		return err
	}
	defer instanceStore.Close()

	ref, err := resolveWorkItemRef(input, instanceStore)
	if err != nil {
		return err
	}
	provider, err := workItemProviderForRef(options, ref)
	if err != nil {
		return err
	}
	var workItem store.WorkItem
	if snapshotMode == refreshSelectedWorkItemSnapshot {
		workItem, err = importWorkItemSnapshot(cmd, instanceStore, provider, ref)
	} else {
		workItem, err = getOrImportWorkItem(cmd, instanceStore, provider, ref)
	}
	if err != nil {
		return err
	}
	selectedPreset, err := agentPresetForRun(agentPreset)
	if err != nil {
		return err
	}

	created, err := workflow.CreatePlannedAgentRun(instanceStore, workflow.CreatePlannedAgentRunInput{
		WorkItem:    workItem,
		AgentPreset: selectedPreset,
	})
	if err != nil {
		return err
	}

	paths, err := workspacePathsForRun(created.AgentRun.ID)
	if err != nil {
		return err
	}
	if _, err := workflow.PrepareAgentRunWorkspace(instanceStore, runner.LocalWorkspacePreparer{}, created.AgentRun.ID, paths); err != nil {
		return err
	}

	changeProvider, err := changeProviderForProvider(options, workItem.Provider)
	if err != nil {
		return err
	}
	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		cmd.Context(),
		instanceStore,
		agentCommandPlanner(options),
		agentCommandRunner(options),
		repositoryChangeMaterializer(options),
		changeProvider,
		created.AgentRun.ID,
	)
	if err != nil {
		if result.AgentRun.ID != 0 {
			printStartedAgentRun(stdout, workItem, created.Branch, result, err)
		}
		return err
	}

	printStartedAgentRun(stdout, workItem, created.Branch, result, nil)
	return nil
}

func getOrImportWorkItem(cmd *cobra.Command, instanceStore *store.Store, provider workitems.Provider, ref workitems.ProviderRef) (store.WorkItem, error) {
	workItem, err := instanceStore.GetWorkItemByProviderRef(ref.String())
	if err == nil {
		return workItem, nil
	}
	if !errors.Is(err, store.ErrWorkItemNotFound) {
		return store.WorkItem{}, err
	}

	return importWorkItemSnapshot(cmd, instanceStore, provider, ref)
}

func importWorkItemSnapshot(cmd *cobra.Command, instanceStore *store.Store, provider workitems.Provider, ref workitems.ProviderRef) (store.WorkItem, error) {
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

func newRunsExecuteCommand(stdout io.Writer, options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "execute <run_id>",
		Short: "Execute the AgentAdapter command for a prepared AgentRun.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, err := parseAgentRunID(args[0])
			if err != nil {
				return err
			}

			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			changeProvider, err := changeProviderForRun(options, instanceStore, runID)
			if err != nil {
				return err
			}
			result, err := workflow.ExecuteAgentRunCommandAndDeliver(
				cmd.Context(),
				instanceStore,
				agentCommandPlanner(options),
				agentCommandRunner(options),
				repositoryChangeMaterializer(options),
				changeProvider,
				runID,
			)
			if err != nil {
				return err
			}
			printExecutedAgentRun(stdout, result)
			return nil
		},
	}
}

func newRunsStopCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <run_id>",
		Short: "Request stop for an active AgentRun.",
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

			result, err := workflow.RequestAgentRunStop(instanceStore, runID)
			if err != nil {
				return err
			}
			printStoppedAgentRun(stdout, result)
			return nil
		},
	}
}

func newRunsRequestChangesCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "request-changes <run_id> <message>",
		Short: "Record local requested changes for an active ChangeSet.",
		Args:  cobra.ExactArgs(2),
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

			result, err := workflow.RequestChangeSetChanges(instanceStore, runID, args[1])
			if err != nil {
				return err
			}
			printRequestedChangeSetChanges(stdout, result)
			return nil
		},
	}
}

func newRunsCloseCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "close <run_id> [message]",
		Short: "Close the active local ChangeSet for a terminal AgentRun.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			runID, err := parseAgentRunID(args[0])
			if err != nil {
				return err
			}
			message := ""
			if len(args) == 2 {
				message = args[1]
			}

			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			result, err := workflow.CloseChangeSet(instanceStore, runID, message)
			if err != nil {
				return err
			}
			printClosedChangeSet(stdout, result)
			return nil
		},
	}
}

func newRunsRequestAttentionCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "request-attention <run_id> <feedback|approval> <message>",
		Short: "Record pending user attention for an AgentRun.",
		Args:  cobra.ExactArgs(3),
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

			result, err := workflow.RequestAgentRunAttention(instanceStore, runID, args[1], args[2])
			if err != nil {
				return err
			}
			printRequestedRunAttention(stdout, result)
			return nil
		},
	}
}

func newRunsSendCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "send <run_id> <message>",
		Short: "Send feedback to a pending AgentRun attention request.",
		Args:  cobra.ExactArgs(2),
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

			result, err := workflow.SendAgentRunFeedback(instanceStore, runID, args[1])
			if err != nil {
				return err
			}
			printSentRunFeedback(stdout, result)
			return nil
		},
	}
}

func newRunsApproveCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <run_id> <approve|reject> [message]",
		Short: "Approve or reject a pending AgentRun approval request.",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(_ *cobra.Command, args []string) error {
			runID, err := parseAgentRunID(args[0])
			if err != nil {
				return err
			}
			message := ""
			if len(args) == 3 {
				message = args[2]
			}

			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			result, err := workflow.ResolveAgentRunApproval(instanceStore, runID, args[1], message)
			if err != nil {
				return err
			}
			printResolvedRunApproval(stdout, result)
			return nil
		},
	}
}

func newRunsRetryCommand(stdout io.Writer) *cobra.Command {
	var agentPreset string
	cmd := &cobra.Command{
		Use:   "retry <run_id>",
		Short: "Create a fresh AgentRun retry for a terminal AgentRun.",
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

			result, err := workflow.RequestAgentRunRetry(instanceStore, runID, workflow.RequestAgentRunRetryInput{
				AgentPreset: agentPreset,
			})
			if err != nil {
				return err
			}
			printRetriedAgentRun(stdout, runID, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentPreset, "agent-preset", "", "Override AgentAdapter preset for the retry RunSpec")
	return cmd
}

func agentPresetForRun(explicitPreset string) (string, error) {
	if explicitPreset != "" {
		return explicitPreset, nil
	}
	root, err := workflowcontract.RepositoryRoot("")
	if err != nil {
		if errors.Is(err, workflowcontract.ErrRepositoryRootNotFound) {
			return workflowcontract.Default().Agent.DefaultPreset, nil
		}
		return "", err
	}
	return workflowcontract.DefaultAgentPreset(root)
}

func newRunsLogsCommand(stdout io.Writer) *cobra.Command {
	var stream string

	cmd := &cobra.Command{
		Use:   "logs <run_id>",
		Short: "Show captured AgentRun logs.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			runID, err := parseAgentRunID(args[0])
			if err != nil {
				return err
			}
			if stream != "" && stream != "stdout" && stream != "stderr" {
				return fmt.Errorf("invalid log stream %q", stream)
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
			if detail.Workspace == nil {
				return fmt.Errorf("Workspace not prepared for AgentRun %d", runID)
			}
			segments, err := instanceStore.ListLogSegmentsForAgentRun(runID, stream)
			if err != nil {
				return err
			}
			return printRunLogs(stdout, *detail.Workspace, segments)
		},
	}
	cmd.Flags().StringVar(&stream, "stream", "", "Filter logs to stdout or stderr")
	return cmd
}

func newRunsEvidenceCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "evidence <run_id>",
		Short: "Show delivery evidence for review and debugging.",
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
			segments, err := instanceStore.ListLogSegmentsForAgentRun(runID, "")
			if err != nil {
				return err
			}
			events, err := instanceStore.ListEventsForAgentRun(runID)
			if err != nil {
				return err
			}
			return printRunEvidence(stdout, detail, segments, events)
		},
	}
}

func newRunsReportCommand(stdout io.Writer, options Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "report <run_id>",
		Short: "Show an operator-facing AgentRun report.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			events, err := instanceStore.ListEventsForAgentRun(runID)
			if err != nil {
				return err
			}
			providerPRReport, providerPRWarning := providerPRReportForRun(cmd, options, detail)
			if jsonOutput {
				return printRunReportJSON(stdout, detail, events, providerPRReport, providerPRWarning)
			}
			return printRunReport(stdout, detail, events, providerPRReport, providerPRWarning)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit a stable JSON report")
	return cmd
}

func agentCommandPlanner(options Options) workflow.AgentCommandPlanner {
	if options.AgentCommandPlanner != nil {
		return options.AgentCommandPlanner
	}
	return commandadapter.Adapter{
		Secrets: commandadapter.EnvSecretStore{},
	}
}

func agentCommandRunner(options Options) workflow.AgentCommandRunner {
	if options.AgentCommandRunner != nil {
		return options.AgentCommandRunner
	}
	return processrunner.Runner{}
}

func repositoryChangeMaterializer(options Options) workflow.RepositoryChangeMaterializer {
	if options.RepositoryChangeMaterializer != nil {
		return options.RepositoryChangeMaterializer
	}
	return runner.GitCommitMaterializer{}
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

func printStartedAgentRun(stdout io.Writer, workItem store.WorkItem, branch string, result store.AgentRunPrepareResult, deliveryErr error) {
	fmt.Fprintf(stdout, "Started AgentRun %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "WorkItem: %s\n", workItem.ProviderRef)
	fmt.Fprintf(stdout, "Status: %s\n", result.AgentRun.Status)
	fmt.Fprintf(stdout, "Branch: %s\n", branch)
	if deliveryErr != nil {
		fmt.Fprintf(stdout, "Delivery: failed (%s)\n", deliveryErr)
	} else if hasEventType(result.Events, "repository_delivery.skipped") {
		fmt.Fprintln(stdout, "Delivery: skipped (no repository changes)")
	} else if result.ChangeSet != nil && result.ChangeSet.ChangeRef != "" {
		fmt.Fprintln(stdout, "Delivery: draft PR ready")
	} else if result.ChangeSet != nil && result.ChangeSet.BranchProviderRef != "" {
		fmt.Fprintln(stdout, "Delivery: branch ready")
	} else if result.ChangeSet != nil {
		fmt.Fprintln(stdout, "Delivery: changes pending provider delivery")
	}
	for _, ref := range result.CommitRefs {
		fmt.Fprintf(stdout, "Commit: %s@%s %s\n", ref.RepositoryRef, ref.SHA, ref.Subject)
	}
	if result.ChangeSet != nil {
		fmt.Fprintf(stdout, "ChangeSet ID: %d\n", result.ChangeSet.ID)
		if result.ChangeSet.BranchProviderRef != "" {
			fmt.Fprintf(stdout, "Provider branch: %s\n", result.ChangeSet.BranchProviderRef)
		}
		if result.ChangeSet.ChangeRef != "" {
			fmt.Fprintf(stdout, "Draft PR: %s\n", result.ChangeSet.ChangeRef)
		}
	}
	fmt.Fprintf(stdout, "Next: forgelane runs evidence %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Next: forgelane runs show %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Next: forgelane runs logs %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Next: forgelane runs stop %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Next: forgelane runs retry %d\n", result.AgentRun.ID)
}

func printExecutedAgentRun(stdout io.Writer, result store.AgentRunPrepareResult) {
	fmt.Fprintf(stdout, "Executed AgentRun %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "RunnerJob ID: %d\n", result.RunnerJob.ID)
	fmt.Fprintf(stdout, "Status: %s\n", result.AgentRun.Status)
	if hasEventType(result.Events, "repository_delivery.skipped") {
		fmt.Fprintln(stdout, "Delivery: skipped (no repository changes)")
	}
	for _, ref := range result.CommitRefs {
		fmt.Fprintf(stdout, "Commit: %s@%s %s\n", ref.RepositoryRef, ref.SHA, ref.Subject)
	}
	if result.ChangeSet != nil {
		fmt.Fprintf(stdout, "ChangeSet: %d %s %s\n", result.ChangeSet.ID, result.ChangeSet.Status, result.ChangeSet.BranchRef)
		printChangeSetProviderRefs(stdout, *result.ChangeSet)
	}
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printStoppedAgentRun(stdout io.Writer, result workflow.AgentRunControlResult) {
	fmt.Fprintf(stdout, "Stop requested for AgentRun %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Status: %s\n", result.AgentRun.Status)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printRequestedChangeSetChanges(stdout io.Writer, result workflow.ChangeSetControlResult) {
	fmt.Fprintf(stdout, "Requested changes for ChangeSet %d\n", result.ChangeSet.ID)
	fmt.Fprintf(stdout, "AgentRun: %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Status: %s\n", result.ChangeSet.Status)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", result.ChangeSet.BranchRef)
	printChangeSetProviderRefs(stdout, result.ChangeSet)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printClosedChangeSet(stdout io.Writer, result workflow.ChangeSetControlResult) {
	fmt.Fprintf(stdout, "Closed ChangeSet %d\n", result.ChangeSet.ID)
	fmt.Fprintf(stdout, "AgentRun: %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Status: %s\n", result.ChangeSet.Status)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", result.ChangeSet.BranchRef)
	printChangeSetProviderRefs(stdout, result.ChangeSet)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printRequestedRunAttention(stdout io.Writer, result workflow.AgentRunAttentionResult) {
	fmt.Fprintf(stdout, "Requested %s for AgentRun %d\n", result.Attention.Kind, result.AgentRun.ID)
	fmt.Fprintf(stdout, "Status: %s\n", result.ControlAction.Status)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printSentRunFeedback(stdout io.Writer, result workflow.AgentRunAttentionResponseResult) {
	fmt.Fprintf(stdout, "Sent feedback for AgentRun %d\n", result.AgentRun.ID)
	fmt.Fprintf(stdout, "Resolved attention request: %d\n", result.ResolvedAttention.ControlActionID)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printResolvedRunApproval(stdout io.Writer, result workflow.AgentRunAttentionResponseResult) {
	switch result.Decision {
	case "approve":
		fmt.Fprintf(stdout, "Approved attention request for AgentRun %d\n", result.AgentRun.ID)
	case "reject":
		fmt.Fprintf(stdout, "Rejected attention request for AgentRun %d\n", result.AgentRun.ID)
	default:
		fmt.Fprintf(stdout, "Resolved attention request for AgentRun %d\n", result.AgentRun.ID)
	}
	fmt.Fprintf(stdout, "Resolved attention request: %d\n", result.ResolvedAttention.ControlActionID)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printRetriedAgentRun(stdout io.Writer, priorRunID int64, result workflow.AgentRunCreateResult) {
	fmt.Fprintf(stdout, "Retried AgentRun %d as AgentRun %d\n", priorRunID, result.AgentRun.ID)
	fmt.Fprintf(stdout, "Status: %s\n", result.AgentRun.Status)
	fmt.Fprintf(stdout, "ControlAction ID: %d\n", result.ControlAction.ID)
	fmt.Fprintf(stdout, "RunSpec ID: %d\n", result.RunSpec.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", result.Branch)
	if result.ChangeSet != nil {
		fmt.Fprintf(stdout, "ChangeSet: %d %s %s\n", result.ChangeSet.ID, result.ChangeSet.Status, result.ChangeSet.BranchRef)
		fmt.Fprintf(stdout, "ChangeSet active run: %d\n", result.ChangeSet.ActiveRunID)
		printChangeSetProviderRefs(stdout, *result.ChangeSet)
	}
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
		fmt.Fprintf(stdout, "Event ID: %d\n", event.ID)
	}
}

func printRunLogs(stdout io.Writer, workspace store.Workspace, segments []store.LogSegment) error {
	for _, segment := range segments {
		path := filepath.Join(workspace.Paths.Root, segment.ArtifactPath)
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s log segment %d: %w", segment.Stream, segment.ID, err)
		}
		if _, err := file.Seek(segment.ByteStart, io.SeekStart); err != nil {
			file.Close()
			return fmt.Errorf("seek %s log segment %d: %w", segment.Stream, segment.ID, err)
		}
		if _, err := io.CopyN(stdout, file, segment.ByteEnd-segment.ByteStart); err != nil {
			file.Close()
			return fmt.Errorf("read %s log segment %d: %w", segment.Stream, segment.ID, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close %s log file: %w", segment.Stream, err)
		}
	}
	return nil
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
	if detail.DeliverySkipped {
		fmt.Fprintln(stdout, "Delivery: skipped (no repository changes)")
	}
	if len(detail.CommitRefs) > 0 {
		fmt.Fprintln(stdout, "Commit refs:")
		for _, ref := range detail.CommitRefs {
			fmt.Fprintf(stdout, "- %s@%s %s\n", ref.RepositoryRef, ref.SHA, ref.Subject)
		}
	}
	if detail.ChangeSet != nil {
		fmt.Fprintf(stdout, "ChangeSet: %d %s %s\n", detail.ChangeSet.ID, detail.ChangeSet.Status, detail.ChangeSet.BranchRef)
		fmt.Fprintf(stdout, "ChangeSet active run: %d\n", detail.ChangeSet.ActiveRunID)
		fmt.Fprintf(stdout, "ChangeSet commits: %d\n", len(detail.ChangeSet.CommitRefs))
		printChangeSetProviderRefs(stdout, *detail.ChangeSet)
	}
	if len(detail.PendingAttention) > 0 {
		fmt.Fprintln(stdout, "Pending attention:")
		for _, attention := range detail.PendingAttention {
			fmt.Fprintf(stdout, "- %s #%d: %s\n", attention.Kind, attention.ControlActionID, attention.Message)
			switch attention.Kind {
			case "feedback":
				fmt.Fprintf(stdout, "Next: forgelane runs send %d <message>\n", detail.AgentRun.ID)
			case "approval":
				fmt.Fprintf(stdout, "Next: forgelane runs approve %d <approve|reject>\n", detail.AgentRun.ID)
			}
		}
	}
	return nil
}

func printRunEvidence(stdout io.Writer, detail store.AgentRunDetail, segments []store.LogSegment, events []store.Event) error {
	var spec map[string]any
	if err := json.Unmarshal([]byte(detail.RunSpec.SpecJSON), &spec); err != nil {
		return fmt.Errorf("decode RunSpec %d: %w", detail.RunSpec.ID, err)
	}

	fmt.Fprintf(stdout, "Delivery evidence for AgentRun %d\n", detail.AgentRun.ID)
	fmt.Fprintf(stdout, "WorkItem: %s\n", detail.WorkItem.ProviderRef)
	fmt.Fprintf(stdout, "Status: %s\n", detail.AgentRun.Status)
	fmt.Fprintf(stdout, "Branch: %s\n", stringField(spec, "branch"))
	printEvidenceDelivery(stdout, detail, events)
	if len(detail.CommitRefs) == 0 {
		fmt.Fprintln(stdout, "Commit refs: none")
	} else {
		fmt.Fprintln(stdout, "Commit refs:")
		for _, ref := range detail.CommitRefs {
			fmt.Fprintf(stdout, "- %s@%s %s\n", ref.RepositoryRef, ref.SHA, ref.Subject)
		}
	}
	if detail.ChangeSet == nil {
		if detail.DeliverySkipped {
			fmt.Fprintln(stdout, "ChangeSet status: none (delivery skipped)")
		} else {
			fmt.Fprintln(stdout, "ChangeSet status: none")
		}
	} else {
		fmt.Fprintf(stdout, "ChangeSet status: %s\n", detail.ChangeSet.Status)
		if detail.ChangeSet.BranchProviderRef != "" {
			fmt.Fprintf(stdout, "Provider branch: %s\n", detail.ChangeSet.BranchProviderRef)
		}
		if detail.ChangeSet.ChangeRef != "" {
			fmt.Fprintf(stdout, "Draft PR: %s\n", detail.ChangeSet.ChangeRef)
		}
	}
	if detail.Workspace != nil {
		fmt.Fprintf(stdout, "Logs: forgelane runs logs %d\n", detail.AgentRun.ID)
		fmt.Fprintf(stdout, "Workspace logs: %s\n", detail.Workspace.Paths.Logs)
	} else {
		fmt.Fprintln(stdout, "Logs: workspace not prepared")
	}
	if len(segments) == 0 {
		fmt.Fprintln(stdout, "Log previews: none")
	} else {
		fmt.Fprintln(stdout, "Log previews:")
		for _, segment := range evidenceLogSegments(segments) {
			fmt.Fprintf(stdout, "- %s #%d %s: %s\n", segment.Stream, segment.Sequence, segment.ArtifactPath, conciseLogPreview(segment.Preview))
		}
		if len(segments) > maxEvidenceLogPreviewSegments {
			fmt.Fprintf(stdout, "More log segments: %d hidden; use forgelane runs logs %d\n", len(segments)-maxEvidenceLogPreviewSegments, detail.AgentRun.ID)
		}
	}
	evidenceEvents := filteredEvidenceEvents(events)
	if len(evidenceEvents) == 0 {
		fmt.Fprintln(stdout, "Events: none")
	} else {
		fmt.Fprintln(stdout, "Events:")
		for _, event := range evidenceEvents {
			fmt.Fprintf(stdout, "- #%d %s %s\n", event.ID, event.Type, event.SubjectRef)
		}
		if len(evidenceEvents) < len(events) {
			fmt.Fprintf(stdout, "More events: forgelane events list --run %d\n", detail.AgentRun.ID)
		}
	}
	return nil
}

func printRunReport(stdout io.Writer, detail store.AgentRunDetail, events []store.Event, providerPRReport *workflow.ProviderPRReport, providerPRWarning string) error {
	var spec map[string]any
	if err := json.Unmarshal([]byte(detail.RunSpec.SpecJSON), &spec); err != nil {
		return fmt.Errorf("decode RunSpec %d: %w", detail.RunSpec.ID, err)
	}

	fmt.Fprintf(stdout, "Run report for AgentRun %d\n", detail.AgentRun.ID)
	fmt.Fprintf(stdout, "WorkItem: %s\n", detail.WorkItem.ProviderRef)
	fmt.Fprintf(stdout, "WorkItem title: %s\n", detail.WorkItem.Title)
	fmt.Fprintf(stdout, "Run status: %s\n", detail.AgentRun.Status)
	fmt.Fprintf(stdout, "Branch: %s\n", stringField(spec, "branch"))
	if detail.ChangeSet == nil {
		if detail.DeliverySkipped {
			fmt.Fprintln(stdout, "ChangeSet status: none (delivery skipped)")
		} else {
			fmt.Fprintln(stdout, "ChangeSet status: none")
		}
	} else {
		fmt.Fprintf(stdout, "ChangeSet: %d\n", detail.ChangeSet.ID)
		fmt.Fprintf(stdout, "ChangeSet status: %s\n", detail.ChangeSet.Status)
		if detail.ChangeSet.BranchProviderRef != "" {
			fmt.Fprintf(stdout, "Provider branch: %s\n", detail.ChangeSet.BranchProviderRef)
		}
		if detail.ChangeSet.ChangeRef != "" {
			fmt.Fprintf(stdout, "PR: %s\n", detail.ChangeSet.ChangeRef)
		}
	}
	checkStatus := runCheckStatusForReport(providerPRReport, providerPRWarning)
	fmt.Fprintf(stdout, "Check status: %s\n", checkStatus.Status)
	fmt.Fprintf(stdout, "Check source: %s\n", checkStatus.Source)
	if checkStatus.Warning != "" {
		fmt.Fprintf(stdout, "Check warning: %s\n", checkStatus.Warning)
	}
	if len(detail.CommitRefs) == 0 {
		fmt.Fprintln(stdout, "Commits: none")
	} else {
		fmt.Fprintln(stdout, "Commits:")
		for _, ref := range detail.CommitRefs {
			fmt.Fprintf(stdout, "- %s@%s %s\n", ref.RepositoryRef, ref.SHA, ref.Subject)
		}
	}
	if detail.Workspace != nil {
		fmt.Fprintf(stdout, "Logs: forgelane runs logs %d\n", detail.AgentRun.ID)
		fmt.Fprintf(stdout, "Workspace logs: %s\n", detail.Workspace.Paths.Logs)
	} else {
		fmt.Fprintln(stdout, "Logs: workspace not prepared")
	}
	keyEvents := filteredEvidenceEvents(events)
	if len(keyEvents) == 0 {
		fmt.Fprintln(stdout, "Key events: none")
		return nil
	}
	fmt.Fprintln(stdout, "Key events:")
	for _, event := range keyEvents {
		fmt.Fprintf(stdout, "- #%d %s %s\n", event.ID, event.Type, event.SubjectRef)
	}
	return nil
}

type runReport struct {
	AgentRun    runReportAgentRun    `json:"agent_run"`
	WorkItem    runReportWorkItem    `json:"work_item"`
	ChangeSet   *runReportChangeSet  `json:"change_set"`
	CheckStatus runReportCheckStatus `json:"check_status"`
	ProviderPR  *prReportProviderPR  `json:"provider_pr"`
	Commits     []runReportCommit    `json:"commits"`
	Logs        runReportLogs        `json:"logs"`
	KeyEvents   []runReportEvent     `json:"key_events"`
	Warnings    []string             `json:"warnings"`
}

type runReportAgentRun struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Branch    string `json:"branch"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type runReportWorkItem struct {
	ID          int64  `json:"id"`
	ProviderRef string `json:"provider_ref"`
	Title       string `json:"title"`
	Status      string `json:"status"`
}

type runReportChangeSet struct {
	ID             int64  `json:"id"`
	Status         string `json:"status"`
	Branch         string `json:"branch"`
	ProviderBranch string `json:"provider_branch"`
	PRRef          string `json:"pr_ref"`
	Draft          bool   `json:"draft"`
	ActiveRunID    int64  `json:"active_run_id"`
}

type runReportCheckStatus struct {
	Status  string `json:"status"`
	Source  string `json:"source"`
	Warning string `json:"warning,omitempty"`
}

type runReportCommit struct {
	RepositoryRef string `json:"repository_ref"`
	SHA           string `json:"sha"`
	Subject       string `json:"subject"`
	AuthorName    string `json:"author_name"`
	AuthorEmail   string `json:"author_email"`
}

type runReportLogs struct {
	Command       string `json:"command"`
	WorkspaceLogs string `json:"workspace_logs"`
}

type runReportEvent struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	SubjectRef string `json:"subject_ref"`
}

func printRunReportJSON(stdout io.Writer, detail store.AgentRunDetail, events []store.Event, providerPRReport *workflow.ProviderPRReport, providerPRWarning string) error {
	report, err := buildRunReport(detail, events, providerPRReport, providerPRWarning)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	return encoder.Encode(report)
}

func buildRunReport(detail store.AgentRunDetail, events []store.Event, providerPRReport *workflow.ProviderPRReport, providerPRWarning string) (runReport, error) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(detail.RunSpec.SpecJSON), &spec); err != nil {
		return runReport{}, fmt.Errorf("decode RunSpec %d: %w", detail.RunSpec.ID, err)
	}
	report := runReport{
		AgentRun: runReportAgentRun{
			ID:        detail.AgentRun.ID,
			Status:    detail.AgentRun.Status,
			Branch:    stringField(spec, "branch"),
			CreatedAt: detail.AgentRun.CreatedAt,
			UpdatedAt: detail.AgentRun.UpdatedAt,
		},
		WorkItem: runReportWorkItem{
			ID:          detail.WorkItem.ID,
			ProviderRef: detail.WorkItem.ProviderRef,
			Title:       detail.WorkItem.Title,
			Status:      detail.WorkItem.Status,
		},
		Commits:     make([]runReportCommit, 0, len(detail.CommitRefs)),
		KeyEvents:   make([]runReportEvent, 0, len(events)),
		CheckStatus: runCheckStatusForReport(providerPRReport, providerPRWarning),
		Warnings:    runReportWarnings(providerPRReport, providerPRWarning),
	}
	if detail.ChangeSet != nil {
		report.ChangeSet = &runReportChangeSet{
			ID:             detail.ChangeSet.ID,
			Status:         detail.ChangeSet.Status,
			Branch:         detail.ChangeSet.BranchRef,
			ProviderBranch: detail.ChangeSet.BranchProviderRef,
			PRRef:          detail.ChangeSet.ChangeRef,
			Draft:          detail.ChangeSet.ChangeDraft,
			ActiveRunID:    detail.ChangeSet.ActiveRunID,
		}
	}
	if providerPRReport != nil {
		checkStatus := providerPRReport.CheckStatus
		if checkStatus == "" {
			checkStatus = "unknown"
		}
		report.ProviderPR = &prReportProviderPR{
			Ref:          providerPRReport.Ref,
			Provider:     providerPRReport.Provider,
			Repository:   providerPRReport.Repository,
			Number:       providerPRReport.Number,
			Title:        providerPRReport.Title,
			State:        providerPRReport.State,
			Draft:        providerPRReport.Draft,
			URL:          providerPRReport.URL,
			HeadSHA:      providerPRReport.HeadSHA,
			CheckStatus:  checkStatus,
			CheckWarning: providerPRReport.CheckWarning,
		}
	}
	for _, ref := range detail.CommitRefs {
		report.Commits = append(report.Commits, runReportCommit{
			RepositoryRef: ref.RepositoryRef,
			SHA:           ref.SHA,
			Subject:       ref.Subject,
			AuthorName:    ref.AuthorName,
			AuthorEmail:   ref.AuthorEmail,
		})
	}
	if detail.Workspace != nil {
		report.Logs = runReportLogs{
			Command:       fmt.Sprintf("forgelane runs logs %d", detail.AgentRun.ID),
			WorkspaceLogs: detail.Workspace.Paths.Logs,
		}
	}
	for _, event := range filteredEvidenceEvents(events) {
		report.KeyEvents = append(report.KeyEvents, runReportEvent{
			ID:         event.ID,
			Type:       event.Type,
			SubjectRef: event.SubjectRef,
		})
	}
	return report, nil
}

func runCheckStatusForReport(providerPRReport *workflow.ProviderPRReport, providerPRWarning string) runReportCheckStatus {
	if providerPRReport == nil {
		source := "none"
		if providerPRWarning != "" {
			source = "provider_pr_lookup"
		}
		return runReportCheckStatus{
			Status:  "unknown",
			Source:  source,
			Warning: providerPRWarning,
		}
	}
	status := providerPRReport.CheckStatus
	if status == "" {
		status = "unknown"
	}
	return runReportCheckStatus{
		Status:  status,
		Source:  "provider_pr",
		Warning: providerPRReport.CheckWarning,
	}
}

func providerPRReportForRun(cmd *cobra.Command, options Options, detail store.AgentRunDetail) (*workflow.ProviderPRReport, string) {
	if detail.ChangeSet == nil || detail.ChangeSet.ChangeRef == "" {
		return nil, ""
	}
	ref, err := workflow.ParseProviderPRRef(detail.ChangeSet.ChangeRef)
	if err != nil {
		return nil, err.Error()
	}
	reporter, err := changeReporterForProvider(options, ref.Provider)
	if err != nil {
		return nil, err.Error()
	}
	providerReport, err := reporter.GetProviderPR(cmd.Context(), ref)
	if err != nil {
		return nil, err.Error()
	}
	return &providerReport, ""
}

func runReportWarnings(providerPRReport *workflow.ProviderPRReport, providerPRWarning string) []string {
	warnings := []string{}
	if providerPRWarning != "" {
		warnings = append(warnings, providerPRWarning)
	}
	if providerPRReport != nil && providerPRReport.CheckWarning != "" {
		warnings = append(warnings, providerPRReport.CheckWarning)
	}
	return warnings
}

func printEvidenceDelivery(stdout io.Writer, detail store.AgentRunDetail, events []store.Event) {
	if detail.DeliverySkipped {
		reason := detail.DeliverySkipReason
		if reason == "" {
			reason = "reason missing"
		}
		fmt.Fprintf(stdout, "Delivery: skipped (%s)\n", reason)
		return
	}
	if detail.ChangeSet == nil {
		fmt.Fprintln(stdout, "Delivery: no ChangeSet")
		return
	}
	switch detail.ChangeSet.Status {
	case "draft_open":
		fmt.Fprintln(stdout, "Delivery: draft PR ready")
	case "branch_ready":
		if hasEventType(events, "change_set.draft_pr_failed") {
			fmt.Fprintln(stdout, "Delivery: failed draft PR")
		} else if detail.ChangeSet.ChangeRef != "" {
			fmt.Fprintln(stdout, "Delivery: branch ready")
		} else {
			fmt.Fprintln(stdout, "Delivery: pending draft PR")
		}
	case "branch_push_failed":
		fmt.Fprintln(stdout, "Delivery: failed branch push")
	case "planned":
		fmt.Fprintln(stdout, "Delivery: changes pending provider delivery")
	default:
		fmt.Fprintf(stdout, "Delivery: %s\n", detail.ChangeSet.Status)
	}
}

func conciseLogPreview(preview string) string {
	preview = strings.TrimSpace(preview)
	preview = strings.ReplaceAll(preview, "\r\n", "\n")
	preview = strings.ReplaceAll(preview, "\n", " | ")
	if preview == "" {
		return "(empty)"
	}
	if len(preview) > maxEvidenceLogPreviewBytes {
		return preview[:maxEvidenceLogPreviewBytes] + "..."
	}
	return preview
}

const (
	maxEvidenceLogPreviewBytes    = 200
	maxEvidenceLogPreviewSegments = 3
)

func evidenceLogSegments(segments []store.LogSegment) []store.LogSegment {
	if len(segments) <= maxEvidenceLogPreviewSegments {
		return segments
	}
	return segments[:maxEvidenceLogPreviewSegments]
}

func filteredEvidenceEvents(events []store.Event) []store.Event {
	filtered := make([]store.Event, 0, len(events))
	for _, event := range events {
		if isEvidenceEventType(event.Type) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func isEvidenceEventType(eventType string) bool {
	switch eventType {
	case "agent_command.started",
		"agent_command.completed",
		"agent_command.failed",
		"agent_command.timed_out",
		"agent_command.cancelled",
		"repository_delivery.skipped",
		"repository_commit.materialized",
		"change_set.created",
		"change_set.claimed",
		"change_set.branch_push_started",
		"change_set.branch_push_succeeded",
		"change_set.branch_push_failed",
		"change_set.draft_pr_started",
		"change_set.draft_pr_succeeded",
		"change_set.draft_pr_failed":
		return true
	default:
		return false
	}
}

func printChangeSetProviderRefs(stdout io.Writer, changeSet store.ChangeSet) {
	if changeSet.BranchProviderRef != "" {
		fmt.Fprintf(stdout, "ChangeSet provider branch: %s\n", changeSet.BranchProviderRef)
	}
	if changeSet.ChangeRef != "" {
		fmt.Fprintf(stdout, "ChangeSet provider change: %s\n", changeSet.ChangeRef)
		fmt.Fprintf(stdout, "ChangeSet draft: %t\n", changeSet.ChangeDraft)
	}
}

func hasEventType(events []store.Event, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
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
