package github

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

func TestChangeFeedbackProviderFiltersActionableGitHubFeedback(t *testing.T) {
	requestPaths := make([]string, 0)
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if got := r.Header.Get("Authorization"); got != "Bearer provider-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		requestPaths = append(requestPaths, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/repos/owner/repo/pulls/10":
			return jsonResponse(http.StatusOK, `{"head":{"sha":"headsha"}}`)
		case "/repos/owner/repo/pulls/10/reviews":
			return jsonResponse(http.StatusOK, `[
				{"id":1,"state":"CHANGES_REQUESTED","body":"Old changes","commit_id":"headsha","user":{"login":"octocat"}},
				{"id":2,"state":"APPROVED","body":"Looks good","commit_id":"headsha","user":{"login":"octocat"}},
				{"id":3,"state":"CHANGES_REQUESTED","body":"Please add retry coverage","commit_id":"headsha","user":{"login":"hubot"}},
				{"id":4,"state":"COMMENTED","body":"Non-decision follow-up","commit_id":"headsha","user":{"login":"hubot"}}
			]`)
		case "/graphql":
			if r.Method != http.MethodPost {
				t.Fatalf("expected GraphQL POST, got %s", r.Method)
			}
			return jsonResponse(http.StatusOK, `{
				"data": {
					"repository": {
						"pullRequest": {
							"reviewThreads": {
								"pageInfo": {"hasNextPage": false, "endCursor": ""},
								"nodes": [
									{
										"isResolved": false,
										"isOutdated": false,
										"path": "internal/cli/pr.go",
										"line": 27,
										"comments": {
											"nodes": [
												{"databaseId": 4, "body": "Fix retry output", "path": "internal/cli/pr.go", "line": 27, "commit": {"oid": "headsha"}, "url": "https://github.com/owner/repo/pull/10#discussion_r4"}
											]
										}
									},
									{
										"isResolved": false,
										"isOutdated": true,
										"path": "internal/cli/pr.go",
										"line": 12,
										"comments": {
											"nodes": [
												{"databaseId": 5, "body": "Old comment", "path": "internal/cli/pr.go", "line": 12, "commit": {"oid": "oldsha"}, "url": "https://github.com/owner/repo/pull/10#discussion_r5"}
											]
										}
									},
									{
										"isResolved": true,
										"isOutdated": false,
										"path": "internal/cli/pr.go",
										"line": 14,
										"comments": {
											"nodes": [
												{"databaseId": 6, "body": "Resolved comment", "path": "internal/cli/pr.go", "line": 14, "commit": {"oid": "headsha"}, "url": "https://github.com/owner/repo/pull/10#discussion_r6"}
											]
										}
									},
									{
										"isResolved": true,
										"isOutdated": true,
										"path": "internal/cli/pr.go",
										"comments": {
											"nodes": [
												{"databaseId": 12, "body": "Resolved old-position comment", "path": "internal/cli/pr.go", "originalLine": 15, "commit": {"oid": "headsha"}, "url": "https://github.com/owner/repo/pull/10#discussion_r12"}
											]
										}
									}
								]
							}
						}
					}
				}
			}`)
		case "/repos/owner/repo/commits/headsha/check-runs":
			return jsonResponse(http.StatusOK, `{
				"check_runs": [
					{"id":7,"name":"go test","status":"completed","conclusion":"failure","head_sha":"headsha","output":{"title":"Tests failed","summary":"go test ./... failed"}},
					{"id":8,"name":"lint","status":"completed","conclusion":"success","head_sha":"headsha"},
					{"id":9,"name":"old ci","status":"completed","conclusion":"failure","head_sha":"oldsha"}
				]
			}`)
		case "/repos/owner/repo/commits/headsha/status":
			return jsonResponse(http.StatusOK, `{
				"sha":"headsha",
				"statuses": [
					{"id":10,"context":"legacy-ci","state":"failure","description":"legacy CI failed"},
					{"id":11,"context":"docs","state":"success","description":"docs passed"}
				]
			}`)
		default:
			t.Fatalf("unexpected GitHub request path %s", r.URL.String())
			return jsonResponse(http.StatusNotFound, `{}`)
		}
	})
	provider := NewChangeFeedbackProvider(ChangeFeedbackProviderOptions{
		BaseURL: "https://api.github.test",
		Token:   "provider-token",
		Client:  client,
	})

	snapshot, err := provider.ReadChangeFeedback(context.Background(), workflow.ChangeFeedbackReadPlan{
		RepositoryRef: "github://github.com/owner/repo",
		ChangeRef:     "github://github.com/owner/repo/pulls/10",
	})
	if err != nil {
		t.Fatalf("read Change feedback: %v", err)
	}
	if snapshot.ChangeRef != "github://github.com/owner/repo/pulls/10" || snapshot.HeadSHA != "headsha" {
		t.Fatalf("unexpected snapshot identity %#v", snapshot)
	}

	actionable := workflow.ActionableChangeFeedbackItems(snapshot.Items)
	gotKinds := make([]string, 0, len(actionable))
	for _, item := range actionable {
		gotKinds = append(gotKinds, item.Kind)
		if strings.Contains(item.ProviderRef, "old ci") || item.CommitSHA == "oldsha" {
			t.Fatalf("superseded feedback should not be actionable: %#v", item)
		}
	}
	wantKinds := []string{"requested_changes", "review_comment", "check_run", "commit_status"}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("unexpected actionable feedback kinds got=%#v want=%#v; all items=%#v", gotKinds, wantKinds, snapshot.Items)
	}
	if len(snapshot.Items) != 10 {
		t.Fatalf("expected compact snapshot to retain 10 current/non-current items for audit, got %d: %#v", len(snapshot.Items), snapshot.Items)
	}
	var resolvedWithoutPosition workflow.ChangeFeedbackItem
	for _, item := range snapshot.Items {
		if item.ProviderRef == "github://github.com/owner/repo/pulls/10/comments/12" {
			resolvedWithoutPosition = item
			break
		}
	}
	if resolvedWithoutPosition.ProviderRef == "" {
		t.Fatalf("expected resolved no-position comment to be retained, got %#v", snapshot.Items)
	}
	if resolvedWithoutPosition.Actionable || resolvedWithoutPosition.State != "resolved" {
		t.Fatalf("expected resolved no-position comment to be non-actionable resolved, got %#v", resolvedWithoutPosition)
	}
}

func TestChangeFeedbackProviderPaginatesGitHubFeedbackReads(t *testing.T) {
	graphQLCalls := 0
	seenSecondReviewPage := false
	seenSecondCheckRunPage := false
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		switch r.URL.Path {
		case "/repos/owner/repo/pulls/10":
			return jsonResponse(http.StatusOK, `{"head":{"sha":"headsha"}}`)
		case "/repos/owner/repo/pulls/10/reviews":
			if strings.Contains(r.URL.RawQuery, "page=2") {
				seenSecondReviewPage = true
				return jsonResponse(http.StatusOK, `[
					{"id":2,"state":"CHANGES_REQUESTED","body":"second review page","commit_id":"headsha","user":{"login":"hubot"}}
				]`)
			}
			response := jsonResponse(http.StatusOK, `[
				{"id":1,"state":"COMMENTED","body":"first review page","commit_id":"headsha","user":{"login":"octocat"}}
			]`)
			response.Header.Set("Link", `<https://api.github.test/repos/owner/repo/pulls/10/reviews?per_page=100&page=2>; rel="next"`)
			return response
		case "/graphql":
			graphQLCalls++
			if graphQLCalls == 1 {
				return jsonResponse(http.StatusOK, `{
					"data": {"repository": {"pullRequest": {"reviewThreads": {
						"pageInfo": {"hasNextPage": true, "endCursor": "cursor-1"},
						"nodes": [{"isResolved": false, "isOutdated": false, "path": "a.go", "line": 1, "comments": {"nodes": [
							{"databaseId": 3, "body": "first thread page", "path": "a.go", "line": 1, "commit": {"oid": "headsha"}}
						]}}]
					}}}}
				}`)
			}
			return jsonResponse(http.StatusOK, `{
				"data": {"repository": {"pullRequest": {"reviewThreads": {
					"pageInfo": {"hasNextPage": false, "endCursor": ""},
					"nodes": [{"isResolved": false, "isOutdated": false, "path": "b.go", "line": 2, "comments": {"nodes": [
						{"databaseId": 4, "body": "second thread page", "path": "b.go", "line": 2, "commit": {"oid": "headsha"}}
					]}}]
				}}}}
			}`)
		case "/repos/owner/repo/commits/headsha/check-runs":
			if strings.Contains(r.URL.RawQuery, "page=2") {
				seenSecondCheckRunPage = true
				return jsonResponse(http.StatusOK, `{"check_runs": [
					{"id":6,"name":"second check page","status":"completed","conclusion":"failure","head_sha":"headsha"}
				]}`)
			}
			response := jsonResponse(http.StatusOK, `{"check_runs": [
				{"id":5,"name":"first check page","status":"completed","conclusion":"failure","head_sha":"headsha"}
			]}`)
			response.Header.Set("Link", `<https://api.github.test/repos/owner/repo/commits/headsha/check-runs?filter=latest&per_page=100&page=2>; rel="next"`)
			return response
		case "/repos/owner/repo/commits/headsha/status":
			return jsonResponse(http.StatusOK, `{"sha":"headsha","statuses":[]}`)
		default:
			t.Fatalf("unexpected GitHub request path %s", r.URL.String())
			return jsonResponse(http.StatusNotFound, `{}`)
		}
	})
	provider := NewChangeFeedbackProvider(ChangeFeedbackProviderOptions{
		BaseURL: "https://api.github.test",
		Client:  client,
	})

	snapshot, err := provider.ReadChangeFeedback(context.Background(), workflow.ChangeFeedbackReadPlan{
		RepositoryRef: "github://github.com/owner/repo",
		ChangeRef:     "github://github.com/owner/repo/pulls/10",
	})
	if err != nil {
		t.Fatalf("read Change feedback: %v", err)
	}
	if !seenSecondReviewPage || graphQLCalls != 2 || !seenSecondCheckRunPage {
		t.Fatalf("expected paginated review/thread/check reads, got secondReview=%v graphQLCalls=%d secondCheck=%v", seenSecondReviewPage, graphQLCalls, seenSecondCheckRunPage)
	}
	actionable := workflow.ActionableChangeFeedbackItems(snapshot.Items)
	if len(actionable) != 5 {
		t.Fatalf("expected actionable feedback from every page, got %d: %#v", len(actionable), actionable)
	}
}
