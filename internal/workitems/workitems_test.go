package workitems

import (
	"testing"
	"time"
)

func TestNewWorkItemImportNormalizesProviderIssue(t *testing.T) {
	importDecision, err := NewWorkItemImport(ProviderIssue{
		ProviderRef:       "github://github.com/owner/repo/issues/123",
		Title:             "Import a provider-owned issue",
		Status:            "triaged",
		RawStatus:         "triaged",
		URL:               "https://github.com/owner/repo/issues/123",
		ProviderUpdatedAt: time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new WorkItem import: %v", err)
	}

	if importDecision.Ref.String() != "github://github.com/owner/repo/issues/123" {
		t.Fatalf("unexpected ProviderRef %s", importDecision.Ref.String())
	}
	if importDecision.Issue.RepositoryRef != "github://github.com/owner/repo" {
		t.Fatalf("unexpected RepositoryRef %s", importDecision.Issue.RepositoryRef)
	}
	if importDecision.Issue.ProviderIssueNumber != 123 {
		t.Fatalf("unexpected issue number %d", importDecision.Issue.ProviderIssueNumber)
	}
	if importDecision.Issue.Status != "unknown" {
		t.Fatalf("expected normalized unknown status, got %q", importDecision.Issue.Status)
	}
	if importDecision.Issue.RawStatus != "triaged" {
		t.Fatalf("expected raw status to be preserved, got %q", importDecision.Issue.RawStatus)
	}
}

func TestParseProviderRefAcceptsGitLabIssueRefs(t *testing.T) {
	ref, err := ParseProviderRef("gitlab://gitlab.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("parse GitLab ProviderRef: %v", err)
	}

	if ref.Provider != "gitlab" ||
		ref.ProviderHost != "gitlab.com" ||
		ref.RepositoryPath != "group/subgroup/project" ||
		ref.IssueNumber != 456 {
		t.Fatalf("unexpected GitLab ProviderRef: %#v", ref)
	}
	if ref.String() != "gitlab://gitlab.com/group/subgroup/project/issues/456" {
		t.Fatalf("unexpected canonical ref %q", ref.String())
	}
	if ref.RepositoryRef() != "gitlab://gitlab.com/group/subgroup/project" {
		t.Fatalf("unexpected repository ref %q", ref.RepositoryRef())
	}
}

func TestParseProviderRefAcceptsSelfHostedGitLabIssueRefs(t *testing.T) {
	ref, err := ParseProviderRef("gitlab://gitlab.example.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("parse self-hosted GitLab ProviderRef: %v", err)
	}

	if ref.Provider != "gitlab" ||
		ref.ProviderHost != "gitlab.example.com" ||
		ref.RepositoryPath != "group/subgroup/project" ||
		ref.IssueNumber != 456 {
		t.Fatalf("unexpected self-hosted GitLab ProviderRef: %#v", ref)
	}
	if ref.String() != "gitlab://gitlab.example.com/group/subgroup/project/issues/456" {
		t.Fatalf("unexpected canonical ref %q", ref.String())
	}
	if ref.RepositoryRef() != "gitlab://gitlab.example.com/group/subgroup/project" {
		t.Fatalf("unexpected repository ref %q", ref.RepositoryRef())
	}
}

func TestWorkItemImportEventPlanDistinguishesImportAndRefresh(t *testing.T) {
	importDecision, err := NewWorkItemImport(ProviderIssue{
		ProviderRef:       "github://github.com/owner/repo/issues/123",
		ProviderUpdatedAt: time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new WorkItem import: %v", err)
	}

	firstEvent := importDecision.EventPlan(ImportEventInput{
		Existing:          false,
		WorkItemID:        10,
		ForgeProjectID:    20,
		ProviderUpdatedAt: "2026-05-30T09:10:11Z",
	})
	if firstEvent.Type != "work_item.imported" {
		t.Fatalf("expected imported Event, got %s", firstEvent.Type)
	}

	refreshEvent := importDecision.EventPlan(ImportEventInput{
		Existing:          true,
		WorkItemID:        10,
		ForgeProjectID:    20,
		ProviderUpdatedAt: "2026-05-30T09:10:11Z",
	})
	if refreshEvent.Type != "work_item.refreshed" {
		t.Fatalf("expected refreshed Event, got %s", refreshEvent.Type)
	}

	if refreshEvent.SubjectType != "work_item" || refreshEvent.SubjectRef != "github://github.com/owner/repo/issues/123" {
		t.Fatalf("unexpected Event subject: %s %s", refreshEvent.SubjectType, refreshEvent.SubjectRef)
	}
	if got := refreshEvent.Payload["repository_ref"]; got != "github://github.com/owner/repo" {
		t.Fatalf("expected repository_ref payload, got %#v", got)
	}
	if got := refreshEvent.Payload["work_item_id"]; got != int64(10) {
		t.Fatalf("expected work_item_id payload, got %#v", got)
	}
}
