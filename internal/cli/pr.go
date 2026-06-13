package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/spf13/cobra"
)

func newPRCommand(stdout io.Writer, options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Inspect provider PRs, refresh feedback, and retry active ChangeSets.",
	}
	cmd.AddCommand(newPRReportCommand(stdout, options))
	cmd.AddCommand(newPRSyncFeedbackCommand(stdout, options))
	cmd.AddCommand(newPRRetryCommand(stdout, options))
	return cmd
}

func newPRReportCommand(stdout io.Writer, options Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "report <pr_number>",
		Short: "Show an operator-facing provider PR report.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNumber, err := parsePRNumber(args[0])
			if err != nil {
				return err
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
			ref := workflow.ProviderPRRef{
				Provider:       forgeProject.Provider,
				ProviderHost:   forgeProject.ProviderHost,
				RepositoryPath: forgeProject.RepositoryPath,
				Number:         prNumber,
			}
			reporter, err := changeReporterForProvider(options, ref.Provider)
			if err != nil {
				return err
			}
			providerReport, err := reporter.GetProviderPR(cmd.Context(), ref)
			if err != nil {
				return err
			}

			changeSetReport, err := instanceStore.GetChangeSetReportByChangeRef(ref.String())
			if err != nil && !errors.Is(err, store.ErrChangeSetNotFound) {
				return err
			}
			var local *store.ChangeSetReport
			if err == nil {
				local = &changeSetReport
			}
			if jsonOutput {
				return printPRReportJSON(stdout, providerReport, local)
			}
			return printPRReport(stdout, providerReport, local)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit a stable JSON report")
	return cmd
}

func newPRSyncFeedbackCommand(stdout io.Writer, options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "sync-feedback <pr-number>",
		Short: "Refresh compact provider PR feedback for an active ChangeSet.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			prLabel, result, err := syncPRFeedback(cmd, options, instanceStore, args[0])
			if err != nil {
				return err
			}
			printPRFeedbackSync(stdout, prLabel, result)
			return nil
		},
	}
}

func newPRRetryCommand(stdout io.Writer, options Options) *cobra.Command {
	var agentPreset string
	var message string

	cmd := &cobra.Command{
		Use:   "retry <pr-number>",
		Short: "Refresh actionable PR feedback and create a follow-up AgentRun.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			prLabel, syncResult, err := syncPRFeedback(cmd, options, instanceStore, args[0])
			if err != nil {
				return err
			}
			retrySnapshot := syncResult.Snapshot
			actionableItems := workflow.ActionableChangeFeedbackItems(retrySnapshot.Items)
			if len(actionableItems) == 0 {
				if strings.TrimSpace(message) == "" {
					return fmt.Errorf("no actionable Change feedback for PR %s; pass --message to retry intentionally", prLabel)
				}
				retrySnapshot = appendManualChangeFeedback(retrySnapshot, syncResult.ChangeSet, message)
				actionableItems = workflow.ActionableChangeFeedbackItems(retrySnapshot.Items)
			}

			result, err := workflow.RequestAgentRunRetry(instanceStore, syncResult.ChangeSet.ActiveRunID, workflow.RequestAgentRunRetryInput{
				AgentPreset:    agentPreset,
				ChangeSetID:    syncResult.ChangeSet.ID,
				ChangeFeedback: &retrySnapshot,
			})
			if err != nil {
				return err
			}
			printPRRetried(stdout, prLabel, syncResult, result, len(actionableItems))
			return nil
		},
	}
	cmd.Flags().StringVar(&agentPreset, "agent-preset", "", "Agent preset for the follow-up AgentRun")
	cmd.Flags().StringVar(&message, "message", "", "Manual retry request when no provider feedback is actionable")
	return cmd
}

func parsePRNumber(input string) (int, error) {
	number, err := strconv.Atoi(input)
	if err != nil || number <= 0 || strconv.Itoa(number) != input {
		return 0, fmt.Errorf("invalid PR number %q", input)
	}
	return number, nil
}

func printPRReport(stdout io.Writer, providerReport workflow.ProviderPRReport, local *store.ChangeSetReport) error {
	fmt.Fprintf(stdout, "PR report for %s\n", providerReport.Ref)
	fmt.Fprintf(stdout, "Title: %s\n", providerReport.Title)
	fmt.Fprintf(stdout, "Provider state: %s\n", providerReport.State)
	fmt.Fprintf(stdout, "Draft: %t\n", providerReport.Draft)
	if providerReport.URL != "" {
		fmt.Fprintf(stdout, "URL: %s\n", providerReport.URL)
	}
	if providerReport.HeadSHA != "" {
		fmt.Fprintf(stdout, "Head SHA: %s\n", providerReport.HeadSHA)
	}
	checkStatus := providerReport.CheckStatus
	if checkStatus == "" {
		checkStatus = "unknown"
	}
	fmt.Fprintf(stdout, "Check status: %s\n", checkStatus)
	if providerReport.CheckWarning != "" {
		fmt.Fprintf(stdout, "Check warning: %s\n", providerReport.CheckWarning)
	}
	fmt.Fprintf(stdout, "Actionable feedback: %s\n", prReportFeedbackStatusNotAvailable)
	fmt.Fprintf(stdout, "Warning: %s\n", prReportFeedbackNotSyncedWarning)
	if local == nil {
		fmt.Fprintln(stdout, "ChangeSet: none (unmapped)")
		fmt.Fprintln(stdout, "Warning: PR is not mapped to an active ForgeLane ChangeSet for retry")
		return nil
	}

	fmt.Fprintf(stdout, "ChangeSet: %d %s %s\n", local.ChangeSet.ID, local.ChangeSet.Status, local.ChangeSet.BranchRef)
	fmt.Fprintf(stdout, "WorkItem: %s\n", local.WorkItem.ProviderRef)
	if len(local.AgentRuns) == 0 {
		fmt.Fprintln(stdout, "Related AgentRuns: none")
	} else {
		fmt.Fprintln(stdout, "Related AgentRuns:")
		for _, run := range local.AgentRuns {
			fmt.Fprintf(stdout, "- AgentRun %d %s\n", run.ID, run.Status)
		}
	}
	if len(local.CommitRefs) == 0 {
		fmt.Fprintln(stdout, "Commits: none")
	} else {
		fmt.Fprintln(stdout, "Commits:")
		for _, ref := range local.CommitRefs {
			fmt.Fprintf(stdout, "- %s@%s %s\n", ref.RepositoryRef, ref.SHA, ref.Subject)
		}
	}
	if !local.ActiveForRetry {
		fmt.Fprintln(stdout, "Warning: PR is not mapped to an active ForgeLane ChangeSet for retry")
	}
	return nil
}

type prReport struct {
	ProviderPR         prReportProviderPR         `json:"provider_pr"`
	ChangeSet          *prReportChangeSet         `json:"change_set"`
	WorkItem           *runReportWorkItem         `json:"work_item"`
	RelatedAgentRuns   []prReportAgentRun         `json:"related_agent_runs"`
	Commits            []runReportCommit          `json:"commits"`
	ActionableFeedback prReportActionableFeedback `json:"actionable_feedback"`
	Warnings           []string                   `json:"warnings"`
}

type prReportProviderPR struct {
	Ref          string `json:"ref"`
	Provider     string `json:"provider"`
	Repository   string `json:"repository"`
	Number       int    `json:"number"`
	Title        string `json:"title"`
	State        string `json:"state"`
	Draft        bool   `json:"draft"`
	URL          string `json:"url"`
	HeadSHA      string `json:"head_sha"`
	CheckStatus  string `json:"check_status"`
	CheckWarning string `json:"check_warning,omitempty"`
}

type prReportChangeSet struct {
	ID             int64  `json:"id"`
	Status         string `json:"status"`
	Branch         string `json:"branch"`
	ProviderBranch string `json:"provider_branch"`
	PRRef          string `json:"pr_ref"`
	Draft          bool   `json:"draft"`
	ActiveRunID    int64  `json:"active_run_id"`
	ActiveForRetry bool   `json:"active_for_retry"`
}

type prReportAgentRun struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

type prReportActionableFeedback struct {
	Status  string   `json:"status"`
	Items   []string `json:"items"`
	Warning string   `json:"warning"`
}

const (
	prReportFeedbackStatusNotAvailable = "not_available"
	prReportFeedbackNotSyncedWarning   = "Actionable Change feedback is not synced in this issue #49 report slice"
)

func printPRReportJSON(stdout io.Writer, providerReport workflow.ProviderPRReport, local *store.ChangeSetReport) error {
	report := buildPRReport(providerReport, local)
	encoder := json.NewEncoder(stdout)
	return encoder.Encode(report)
}

func buildPRReport(providerReport workflow.ProviderPRReport, local *store.ChangeSetReport) prReport {
	checkStatus := providerReport.CheckStatus
	if checkStatus == "" {
		checkStatus = "unknown"
	}
	report := prReport{
		ProviderPR: prReportProviderPR{
			Ref:          providerReport.Ref,
			Provider:     providerReport.Provider,
			Repository:   providerReport.Repository,
			Number:       providerReport.Number,
			Title:        providerReport.Title,
			State:        providerReport.State,
			Draft:        providerReport.Draft,
			URL:          providerReport.URL,
			HeadSHA:      providerReport.HeadSHA,
			CheckStatus:  checkStatus,
			CheckWarning: providerReport.CheckWarning,
		},
		RelatedAgentRuns: []prReportAgentRun{},
		Commits:          []runReportCommit{},
		ActionableFeedback: prReportActionableFeedback{
			Status:  prReportFeedbackStatusNotAvailable,
			Items:   []string{},
			Warning: prReportFeedbackNotSyncedWarning,
		},
		Warnings: prReportWarnings(providerReport, local),
	}
	if local == nil {
		return report
	}
	report.ChangeSet = &prReportChangeSet{
		ID:             local.ChangeSet.ID,
		Status:         local.ChangeSet.Status,
		Branch:         local.ChangeSet.BranchRef,
		ProviderBranch: local.ChangeSet.BranchProviderRef,
		PRRef:          local.ChangeSet.ChangeRef,
		Draft:          local.ChangeSet.ChangeDraft,
		ActiveRunID:    local.ChangeSet.ActiveRunID,
		ActiveForRetry: local.ActiveForRetry,
	}
	report.WorkItem = &runReportWorkItem{
		ID:          local.WorkItem.ID,
		ProviderRef: local.WorkItem.ProviderRef,
		Title:       local.WorkItem.Title,
		Status:      local.WorkItem.Status,
	}
	for _, run := range local.AgentRuns {
		report.RelatedAgentRuns = append(report.RelatedAgentRuns, prReportAgentRun{
			ID:     run.ID,
			Status: run.Status,
		})
	}
	for _, ref := range local.CommitRefs {
		report.Commits = append(report.Commits, runReportCommit{
			RepositoryRef: ref.RepositoryRef,
			SHA:           ref.SHA,
			Subject:       ref.Subject,
			AuthorName:    ref.AuthorName,
			AuthorEmail:   ref.AuthorEmail,
		})
	}
	return report
}

func prReportWarnings(providerReport workflow.ProviderPRReport, local *store.ChangeSetReport) []string {
	warnings := []string{}
	if providerReport.CheckWarning != "" {
		warnings = append(warnings, providerReport.CheckWarning)
	}
	warnings = append(warnings, prReportFeedbackNotSyncedWarning)
	if local == nil || !local.ActiveForRetry {
		warnings = append(warnings, "PR is not mapped to an active ForgeLane ChangeSet for retry")
	}
	return warnings
}

func syncPRFeedback(cmd *cobra.Command, options Options, instanceStore *store.Store, input string) (string, workflow.ChangeFeedbackSyncResult, error) {
	changeRef, prLabel, err := resolvePRChangeRef(input, instanceStore)
	if err != nil {
		return "", workflow.ChangeFeedbackSyncResult{}, err
	}
	changeSet, err := instanceStore.GetActiveChangeSetByChangeRef(changeRef)
	if errors.Is(err, store.ErrChangeSetNotFound) {
		return "", workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("PR %s is not mapped to an active ChangeSet", prLabel)
	}
	if err != nil {
		return "", workflow.ChangeFeedbackSyncResult{}, err
	}
	provider, err := changeFeedbackProviderForProvider(options, changeSet.Provider)
	if err != nil {
		return "", workflow.ChangeFeedbackSyncResult{}, err
	}
	if provider == nil {
		return "", workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("missing ChangeFeedbackProvider for provider %q", changeSet.Provider)
	}
	result, err := workflow.SyncChangeFeedback(cmd.Context(), instanceStore, provider, changeSet)
	if err != nil {
		return "", workflow.ChangeFeedbackSyncResult{}, err
	}
	return prLabel, result, nil
}

func resolvePRChangeRef(input string, instanceStore *store.Store) (string, string, error) {
	if isPositiveInteger(input) {
		prNumber, err := strconv.Atoi(input)
		if err != nil || prNumber <= 0 {
			return "", "", fmt.Errorf("invalid PR number %q", input)
		}
		forgeProject, err := repositoryconfig.InferForgeProjectFromOrigin("")
		if err != nil {
			return "", "", fmt.Errorf("%w; pass a full PR ProviderRef or run forgelane init", err)
		}
		changeRef, err := resolvePRNumberFromForgeProject(prNumber, forgeProject, instanceStore)
		if err != nil {
			return "", "", err
		}
		return changeRef, strconv.Itoa(prNumber), nil
	}
	changeRef, prNumber, err := parsePRChangeRef(input)
	if err != nil {
		return "", "", err
	}
	return changeRef, strconv.Itoa(prNumber), nil
}

func resolvePRNumberFromForgeProject(prNumber int, forgeProject repositoryconfig.ForgeProject, instanceStore *store.Store) (string, error) {
	projectRef := repositoryconfig.ForgeProjectRef(forgeProject)
	persistedProject, err := instanceStore.GetForgeProjectByRef(projectRef)
	if err != nil {
		return "", fmt.Errorf("%w; pass a full PR ProviderRef or run forgelane init", err)
	}
	if persistedProject.Provider != "github" {
		return "", fmt.Errorf("PR feedback retry currently supports GitHub ChangeSets; got provider %q", persistedProject.Provider)
	}
	return fmt.Sprintf("%s://%s/%s/pulls/%d", persistedProject.Provider, persistedProject.ProviderHost, persistedProject.RepositoryPath, prNumber), nil
}

func parsePRChangeRef(input string) (string, int, error) {
	parsed, err := url.Parse(input)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", 0, fmt.Errorf("invalid PR ProviderRef %q", input)
	}
	if parsed.Scheme != "github" {
		return "", 0, fmt.Errorf("PR feedback retry currently supports GitHub ChangeSets; got provider %q", parsed.Scheme)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[len(parts)-2] != "pulls" {
		return "", 0, fmt.Errorf("invalid PR ProviderRef %q", input)
	}
	prNumber, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || prNumber <= 0 {
		return "", 0, fmt.Errorf("invalid PR ProviderRef %q", input)
	}
	repositoryPath := strings.Join(parts[:len(parts)-2], "/")
	if repositoryPath == "" {
		return "", 0, fmt.Errorf("invalid PR ProviderRef %q", input)
	}
	return fmt.Sprintf("github://%s/%s/pulls/%d", parsed.Host, repositoryPath, prNumber), prNumber, nil
}

func appendManualChangeFeedback(snapshot workflow.ChangeFeedbackSnapshot, changeSet workflow.ChangeSet, message string) workflow.ChangeFeedbackSnapshot {
	message = strings.TrimSpace(message)
	snapshot.Items = append(snapshot.Items, workflow.ChangeFeedbackItem{
		ProviderRef: fmt.Sprintf("local://forgelane/change-sets/%d/manual-feedback", changeSet.ID),
		Kind:        "manual",
		Actionable:  true,
		Summary:     "Manual retry request",
		Body:        message,
		CommitSHA:   snapshot.HeadSHA,
		State:       "manual",
	})
	return snapshot
}

func printPRFeedbackSync(stdout io.Writer, prLabel string, result workflow.ChangeFeedbackSyncResult) {
	fmt.Fprintf(stdout, "Synced Change feedback for PR %s\n", prLabel)
	printPRFeedbackSummary(stdout, result, len(result.ActionableItems))
}

func printPRRetried(stdout io.Writer, prLabel string, syncResult workflow.ChangeFeedbackSyncResult, retryResult workflow.AgentRunCreateResult, actionableCount int) {
	fmt.Fprintf(stdout, "Retried PR %s as AgentRun %d\n", prLabel, retryResult.AgentRun.ID)
	printPRFeedbackSummary(stdout, syncResult, actionableCount)
	for _, event := range retryResult.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
	}
}

func printPRFeedbackSummary(stdout io.Writer, result workflow.ChangeFeedbackSyncResult, actionableCount int) {
	fmt.Fprintf(stdout, "ChangeSet: %d %s %s\n", result.ChangeSet.ID, result.ChangeSet.Status, result.ChangeSet.BranchRef)
	fmt.Fprintf(stdout, "ChangeRef: %s\n", result.Snapshot.ChangeRef)
	fmt.Fprintf(stdout, "Head: %s\n", result.Snapshot.HeadSHA)
	fmt.Fprintf(stdout, "Feedback items: %d\n", len(result.Items))
	fmt.Fprintf(stdout, "Actionable feedback: %d\n", actionableCount)
	for _, event := range result.Events {
		fmt.Fprintf(stdout, "Event: %s\n", event.Type)
	}
}
