package gitlab

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/liiujinfu/forgelane/internal/workitems"
)

func TestIssueProviderReadsGitLabIssueSnapshot(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsubgroup%2Fproject/issues/456" {
			t.Fatalf("unexpected request path %s", r.URL.EscapedPath())
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "test-token" {
			t.Fatalf("unexpected PRIVATE-TOKEN header %q", got)
		}
		return jsonResponse(http.StatusOK, `{
			"iid": 456,
			"title": "Persist a GitLab WorkItem snapshot",
			"description": "Import the provider-owned GitLab issue.",
			"state": "opened",
			"web_url": "https://gitlab.com/group/subgroup/project/-/issues/456",
			"updated_at": "2026-05-30T09:10:11Z"
		}`)
	})

	provider := NewIssueProvider(Options{
		BaseURL: "https://gitlab.test/api/v4",
		Token:   "test-token",
		Client:  client,
	})
	ref, err := workitems.ParseProviderRef("gitlab://gitlab.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("parse ProviderRef: %v", err)
	}

	issue, err := provider.GetIssue(context.Background(), ref)
	if err != nil {
		t.Fatalf("expected issue read to succeed: %v", err)
	}

	if issue.ProviderRef != "gitlab://gitlab.com/group/subgroup/project/issues/456" {
		t.Fatalf("unexpected ProviderRef %q", issue.ProviderRef)
	}
	if issue.RepositoryRef != "gitlab://gitlab.com/group/subgroup/project" {
		t.Fatalf("unexpected RepositoryRef %q", issue.RepositoryRef)
	}
	if issue.ProviderIssueNumber != 456 {
		t.Fatalf("unexpected issue number %d", issue.ProviderIssueNumber)
	}
	if issue.Title != "Persist a GitLab WorkItem snapshot" || issue.Body != "Import the provider-owned GitLab issue." {
		t.Fatalf("unexpected issue content %#v", issue)
	}
	if issue.Status != "open" || issue.RawStatus != "opened" {
		t.Fatalf("unexpected status %q raw %q", issue.Status, issue.RawStatus)
	}
	if !issue.ProviderUpdatedAt.Equal(time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC)) {
		t.Fatalf("unexpected updated_at %s", issue.ProviderUpdatedAt)
	}
}

func TestIssueProviderPrefersForgeLaneGitLabToken(t *testing.T) {
	t.Setenv("FORGELANE_GITLAB_TOKEN", "forgelane-token")
	t.Setenv("GITLAB_TOKEN", "gitlab-token")

	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "forgelane-token" {
			t.Fatalf("unexpected PRIVATE-TOKEN header %q", got)
		}
		return jsonResponse(http.StatusOK, `{
			"iid": 456,
			"title": "Persist a GitLab WorkItem snapshot",
			"state": "opened",
			"web_url": "https://gitlab.com/group/project/-/issues/456",
			"updated_at": "2026-05-30T09:10:11Z"
		}`)
	})
	provider := NewIssueProvider(Options{
		BaseURL: "https://gitlab.test/api/v4",
		Client:  client,
	})
	ref, err := workitems.ParseProviderRef("gitlab://gitlab.com/group/project/issues/456")
	if err != nil {
		t.Fatalf("parse ProviderRef: %v", err)
	}

	if _, err := provider.GetIssue(context.Background(), ref); err != nil {
		t.Fatalf("read issue: %v", err)
	}
}

func TestIssueProviderDerivesBaseURLFromSelfHostedProviderRef(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.URL.Scheme != "https" || r.URL.Host != "gitlab.example.com" {
			t.Fatalf("unexpected request URL %s", r.URL.String())
		}
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsubgroup%2Fproject/issues/456" {
			t.Fatalf("unexpected request path %s", r.URL.EscapedPath())
		}
		return jsonResponse(http.StatusOK, `{
			"iid": 456,
			"title": "Persist a self-hosted GitLab WorkItem snapshot",
			"state": "opened",
			"web_url": "https://gitlab.example.com/group/subgroup/project/-/issues/456",
			"updated_at": "2026-05-30T09:10:11Z"
		}`)
	})
	provider := NewIssueProvider(Options{
		Token:  "test-token",
		Client: client,
	})
	ref, err := workitems.ParseProviderRef("gitlab://gitlab.example.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("parse ProviderRef: %v", err)
	}

	issue, err := provider.GetIssue(context.Background(), ref)
	if err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if issue.RepositoryRef != "gitlab://gitlab.example.com/group/subgroup/project" {
		t.Fatalf("unexpected RepositoryRef %q", issue.RepositoryRef)
	}
}

func TestIssueProviderClassifiesGitLabFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       string
	}{
		{name: "not found", statusCode: http.StatusNotFound, want: "issue not found"},
		{name: "unauthorized", statusCode: http.StatusUnauthorized, want: "auth or permission failure"},
		{name: "forbidden", statusCode: http.StatusForbidden, want: "auth or permission failure"},
		{name: "generic", statusCode: http.StatusInternalServerError, want: "GitLab provider failure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fakeHTTPClient(func(_ *http.Request) *http.Response {
				return &http.Response{
					StatusCode: tt.statusCode,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("provider error")),
				}
			})
			provider := NewIssueProvider(Options{
				BaseURL: "https://gitlab.test/api/v4",
				Client:  client,
			})
			ref, err := workitems.ParseProviderRef("gitlab://gitlab.com/group/project/issues/456")
			if err != nil {
				t.Fatalf("parse ProviderRef: %v", err)
			}

			_, err = provider.GetIssue(context.Background(), ref)
			if err == nil {
				t.Fatal("expected provider read to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) *http.Response

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request), nil
}

func fakeHTTPClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
