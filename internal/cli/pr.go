package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/spf13/cobra"
)

func newPRCommand(stdout io.Writer, options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Inspect provider PRs.",
	}
	cmd.AddCommand(newPRReportCommand(stdout, options))
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
