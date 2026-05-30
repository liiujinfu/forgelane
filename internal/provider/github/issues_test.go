package github

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

func TestIssueProviderReadsGitHubIssueSnapshot(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.URL.Path != "/repos/owner/repo/issues/123" {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		return jsonResponse(http.StatusOK, `{
			"number": 123,
			"title": "Persist a GitHub WorkItem snapshot",
			"body": "Import the provider-owned issue.",
			"state": "open",
			"html_url": "https://github.com/owner/repo/issues/123",
			"updated_at": "2026-05-30T09:10:11Z"
		}`)
	})

	provider := NewIssueProvider(Options{
		BaseURL: "https://api.github.test",
		Token:   "test-token",
		Client:  client,
	})
	ref, err := workitems.ParseProviderRef("github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("parse ProviderRef: %v", err)
	}

	issue, err := provider.GetIssue(context.Background(), ref)
	if err != nil {
		t.Fatalf("expected issue read to succeed: %v", err)
	}

	if issue.ProviderRef != "github://github.com/owner/repo/issues/123" {
		t.Fatalf("unexpected ProviderRef %q", issue.ProviderRef)
	}
	if issue.RepositoryRef != "github://github.com/owner/repo" {
		t.Fatalf("unexpected RepositoryRef %q", issue.RepositoryRef)
	}
	if issue.ProviderIssueNumber != 123 {
		t.Fatalf("unexpected issue number %d", issue.ProviderIssueNumber)
	}
	if issue.Title != "Persist a GitHub WorkItem snapshot" {
		t.Fatalf("unexpected title %q", issue.Title)
	}
	if issue.Body != "Import the provider-owned issue." {
		t.Fatalf("unexpected body %q", issue.Body)
	}
	if issue.Status != "open" || issue.RawStatus != "open" {
		t.Fatalf("unexpected status %q raw %q", issue.Status, issue.RawStatus)
	}
	if !issue.ProviderUpdatedAt.Equal(time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC)) {
		t.Fatalf("unexpected updated_at %s", issue.ProviderUpdatedAt)
	}
}

func TestIssueProviderRejectsPullRequestIssueResponses(t *testing.T) {
	client := fakeHTTPClient(func(_ *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, `{
			"number": 123,
			"title": "This is really a pull request",
			"state": "open",
			"html_url": "https://github.com/owner/repo/pull/123",
			"updated_at": "2026-05-30T09:10:11Z",
			"pull_request": {"url": "https://api.github.com/repos/owner/repo/pulls/123"}
		}`)
	})

	provider := NewIssueProvider(Options{
		BaseURL: "https://api.github.test",
		Client:  client,
	})
	ref, err := workitems.ParseProviderRef("github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("parse ProviderRef: %v", err)
	}

	_, err = provider.GetIssue(context.Background(), ref)
	if err == nil {
		t.Fatal("expected pull request issue response to fail")
	}
	if !strings.Contains(err.Error(), "not an issue WorkItem") {
		t.Fatalf("expected not-an-issue error, got %v", err)
	}
}

func TestIssueProviderClassifiesProviderFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       string
	}{
		{name: "not found", statusCode: http.StatusNotFound, want: "issue not found"},
		{name: "unauthorized", statusCode: http.StatusUnauthorized, want: "auth or permission failure"},
		{name: "forbidden", statusCode: http.StatusForbidden, want: "auth or permission failure"},
		{name: "generic", statusCode: http.StatusInternalServerError, want: "GitHub provider failure"},
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
				BaseURL: "https://api.github.test",
				Client:  client,
			})
			ref, err := workitems.ParseProviderRef("github://github.com/owner/repo/issues/123")
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
