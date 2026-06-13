package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

// ChangeFeedbackProviderOptions configures GitHub PR feedback reads.
type ChangeFeedbackProviderOptions struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// ChangeFeedbackProvider reads compact feedback from GitHub PR review surfaces.
type ChangeFeedbackProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewChangeFeedbackProvider creates a GitHub ChangeFeedbackProvider.
func NewChangeFeedbackProvider(options ChangeFeedbackProviderOptions) *ChangeFeedbackProvider {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	token := options.Token
	if token == "" {
		token = os.Getenv("FORGELANE_GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &ChangeFeedbackProvider{
		baseURL: baseURL,
		token:   token,
		client:  client,
	}
}

// ReadChangeFeedback reads actionable and non-actionable compact feedback for one GitHub PR.
func (provider *ChangeFeedbackProvider) ReadChangeFeedback(ctx context.Context, plan workflow.ChangeFeedbackReadPlan) (workflow.ChangeFeedbackSnapshot, error) {
	repo, err := parseGitHubRepositoryRef(plan.RepositoryRef)
	if err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}
	pullNumber, err := parseGitHubPullNumber(plan.ChangeRef)
	if err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}

	var pull githubPullPayload
	if err := provider.getJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d", escapeRepositoryPath(repo.path), pullNumber), &pull); err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}
	headSHA := strings.TrimSpace(pull.Head.SHA)
	if headSHA == "" {
		headSHA = plan.HeadSHA
	}
	snapshot := workflow.ChangeFeedbackSnapshot{
		Provider:      "github",
		RepositoryRef: plan.RepositoryRef,
		ChangeRef:     plan.ChangeRef,
		HeadSHA:       headSHA,
	}

	reviewItems, err := provider.readReviewFeedback(ctx, repo.path, pullNumber, headSHA)
	if err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}
	snapshot.Items = append(snapshot.Items, reviewItems...)

	commentItems, err := provider.readReviewCommentFeedback(ctx, repo.path, pullNumber, headSHA)
	if err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}
	snapshot.Items = append(snapshot.Items, commentItems...)

	checkItems, err := provider.readCheckRunFeedback(ctx, repo.path, headSHA)
	if err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}
	snapshot.Items = append(snapshot.Items, checkItems...)

	statusItems, err := provider.readCommitStatusFeedback(ctx, repo.path, headSHA)
	if err != nil {
		return workflow.ChangeFeedbackSnapshot{}, err
	}
	snapshot.Items = append(snapshot.Items, statusItems...)

	return snapshot, nil
}

func (provider *ChangeFeedbackProvider) readReviewFeedback(ctx context.Context, repo string, pullNumber int, headSHA string) ([]workflow.ChangeFeedbackItem, error) {
	reviews, err := getJSONPages[githubReviewPayload](ctx, provider, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", escapeRepositoryPath(repo), pullNumber))
	if err != nil {
		return nil, err
	}
	latestDecisionByReviewer := make(map[string]int64)
	for _, review := range reviews {
		reviewer := review.User.Login
		if reviewer == "" {
			reviewer = fmt.Sprintf("review:%d", review.ID)
		}
		if isReviewDecisionState(review.State) {
			latestDecisionByReviewer[reviewer] = review.ID
		}
	}
	items := make([]workflow.ChangeFeedbackItem, 0, len(reviews))
	for _, review := range reviews {
		reviewer := review.User.Login
		if reviewer == "" {
			reviewer = fmt.Sprintf("review:%d", review.ID)
		}
		if latestDecisionByReviewer[reviewer] != review.ID || !strings.EqualFold(review.State, "CHANGES_REQUESTED") {
			items = append(items, workflow.ChangeFeedbackItem{
				ProviderRef: fmt.Sprintf("github://github.com/%s/pulls/%d/reviews/%d", repo, pullNumber, review.ID),
				Kind:        "review",
				Actionable:  false,
				Summary:     strings.TrimSpace(fmt.Sprintf("%s %s", reviewer, strings.ToLower(review.State))),
				Body:        review.Body,
				CommitSHA:   nonEmptyOr(review.CommitID, headSHA),
				State:       review.State,
				ProviderSnapshot: map[string]any{
					"id":       review.ID,
					"state":    review.State,
					"user":     reviewer,
					"html_url": review.HTMLURL,
				},
			})
			continue
		}
		items = append(items, workflow.ChangeFeedbackItem{
			ProviderRef: fmt.Sprintf("github://github.com/%s/pulls/%d/reviews/%d", repo, pullNumber, review.ID),
			Kind:        "requested_changes",
			Actionable:  true,
			Summary:     fmt.Sprintf("%s requested changes", reviewer),
			Body:        review.Body,
			CommitSHA:   nonEmptyOr(review.CommitID, headSHA),
			State:       review.State,
			ProviderSnapshot: map[string]any{
				"id":       review.ID,
				"state":    review.State,
				"user":     reviewer,
				"html_url": review.HTMLURL,
			},
		})
	}
	return items, nil
}

func (provider *ChangeFeedbackProvider) readReviewCommentFeedback(ctx context.Context, repo string, pullNumber int, headSHA string) ([]workflow.ChangeFeedbackItem, error) {
	owner, name, err := splitGitHubRepository(repo)
	if err != nil {
		return nil, err
	}
	items := make([]workflow.ChangeFeedbackItem, 0)
	after := ""
	for {
		var response githubReviewThreadsGraphQLResponse
		if err := provider.postReviewThreadsGraphQL(ctx, githubReviewThreadsGraphQLRequest{
			Query: githubReviewThreadsQuery,
			Variables: map[string]any{
				"owner":  owner,
				"name":   name,
				"number": pullNumber,
				"after":  emptyStringAsNil(after),
			},
		}, &response); err != nil {
			return nil, err
		}
		threads := response.Data.Repository.PullRequest.ReviewThreads
		for _, thread := range threads.Nodes {
			items = append(items, reviewThreadFeedbackItems(repo, pullNumber, headSHA, thread)...)
		}
		if !threads.PageInfo.HasNextPage {
			break
		}
		after = threads.PageInfo.EndCursor
	}
	return items, nil
}

func (provider *ChangeFeedbackProvider) readCheckRunFeedback(ctx context.Context, repo string, headSHA string) ([]workflow.ChangeFeedbackItem, error) {
	if headSHA == "" {
		return nil, nil
	}
	checkRuns, err := provider.getCheckRunPages(ctx, fmt.Sprintf("/repos/%s/commits/%s/check-runs?filter=latest&per_page=100", escapeRepositoryPath(repo), headSHA))
	if err != nil {
		return nil, err
	}
	items := make([]workflow.ChangeFeedbackItem, 0, len(checkRuns))
	for _, checkRun := range checkRuns {
		if checkRun.HeadSHA != "" && checkRun.HeadSHA != headSHA {
			continue
		}
		conclusion := strings.ToLower(checkRun.Conclusion)
		actionable := checkRun.Status == "completed" && failedGitHubConclusion(conclusion)
		if !actionable {
			continue
		}
		items = append(items, workflow.ChangeFeedbackItem{
			ProviderRef: fmt.Sprintf("github://github.com/%s/check-runs/%d", repo, checkRun.ID),
			Kind:        "check_run",
			Actionable:  true,
			Summary:     strings.TrimSpace(fmt.Sprintf("%s %s", checkRun.Name, conclusion)),
			Body:        strings.TrimSpace(strings.Join([]string{checkRun.Output.Title, checkRun.Output.Summary}, "\n")),
			CommitSHA:   nonEmptyOr(checkRun.HeadSHA, headSHA),
			State:       conclusion,
			ProviderSnapshot: map[string]any{
				"id":          checkRun.ID,
				"name":        checkRun.Name,
				"status":      checkRun.Status,
				"conclusion":  conclusion,
				"html_url":    checkRun.HTMLURL,
				"details_url": checkRun.DetailsURL,
			},
		})
	}
	return items, nil
}

func (provider *ChangeFeedbackProvider) readCommitStatusFeedback(ctx context.Context, repo string, headSHA string) ([]workflow.ChangeFeedbackItem, error) {
	if headSHA == "" {
		return nil, nil
	}
	var payload githubCombinedStatusPayload
	if err := provider.getJSON(ctx, fmt.Sprintf("/repos/%s/commits/%s/status", escapeRepositoryPath(repo), headSHA), &payload); err != nil {
		return nil, err
	}
	if payload.SHA != "" && payload.SHA != headSHA {
		return nil, nil
	}
	items := make([]workflow.ChangeFeedbackItem, 0, len(payload.Statuses))
	for _, status := range payload.Statuses {
		if status.State != "failure" && status.State != "error" {
			continue
		}
		providerRef := fmt.Sprintf("github://github.com/%s/statuses/%s", repo, status.Context)
		if status.ID > 0 {
			providerRef = fmt.Sprintf("github://github.com/%s/statuses/%d", repo, status.ID)
		}
		items = append(items, workflow.ChangeFeedbackItem{
			ProviderRef: providerRef,
			Kind:        "commit_status",
			Actionable:  true,
			Summary:     strings.TrimSpace(fmt.Sprintf("%s %s", status.Context, status.State)),
			Body:        status.Description,
			CommitSHA:   headSHA,
			State:       status.State,
			ProviderSnapshot: map[string]any{
				"id":          status.ID,
				"context":     status.Context,
				"state":       status.State,
				"target_url":  status.TargetURL,
				"description": status.Description,
			},
		})
	}
	return items, nil
}

func (provider *ChangeFeedbackProvider) getJSON(ctx context.Context, path string, target any) error {
	_, err := provider.getJSONWithHeaders(ctx, path, target)
	return err
}

func getJSONPages[T any](ctx context.Context, provider *ChangeFeedbackProvider, path string) ([]T, error) {
	items := make([]T, 0)
	for path != "" {
		var page []T
		header, err := provider.getJSONWithHeaders(ctx, path, &page)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		path = nextGitHubPagePath(header.Get("Link"))
	}
	return items, nil
}

func (provider *ChangeFeedbackProvider) getCheckRunPages(ctx context.Context, path string) ([]githubCheckRunPayload, error) {
	checkRuns := make([]githubCheckRunPayload, 0)
	for path != "" {
		var page githubCheckRunsPayload
		header, err := provider.getJSONWithHeaders(ctx, path, &page)
		if err != nil {
			return nil, err
		}
		checkRuns = append(checkRuns, page.CheckRuns...)
		path = nextGitHubPagePath(header.Get("Link"))
	}
	return checkRuns, nil
}

func (provider *ChangeFeedbackProvider) getJSONWithHeaders(ctx context.Context, path string, target any) (http.Header, error) {
	requestURL := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		requestURL = provider.baseURL + path
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GitHub PR feedback request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if provider.token != "" {
		request.Header.Set("Authorization", "Bearer "+provider.token)
	}

	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitHub PR feedback read failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub PR feedback read failed: HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return nil, fmt.Errorf("decode GitHub PR feedback response: %w", err)
	}
	return response.Header, nil
}

func (provider *ChangeFeedbackProvider) postReviewThreadsGraphQL(ctx context.Context, payload githubReviewThreadsGraphQLRequest, target *githubReviewThreadsGraphQLResponse) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return fmt.Errorf("encode GitHub review threads GraphQL request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.graphQLURL(), &body)
	if err != nil {
		return fmt.Errorf("create GitHub review threads GraphQL request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if provider.token != "" {
		request.Header.Set("Authorization", "Bearer "+provider.token)
	}

	response, err := provider.client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub review threads GraphQL read failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub review threads GraphQL read failed: HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode GitHub review threads GraphQL response: %w", err)
	}
	if len(target.Errors) > 0 {
		return fmt.Errorf("GitHub review threads GraphQL read failed: %s", target.Errors[0].Message)
	}
	return nil
}

func (provider *ChangeFeedbackProvider) graphQLURL() string {
	if strings.HasSuffix(provider.baseURL, "/api/v3") {
		return strings.TrimSuffix(provider.baseURL, "/api/v3") + "/api/graphql"
	}
	return provider.baseURL + "/graphql"
}

type githubPullPayload struct {
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type githubReviewPayload struct {
	ID       int64  `json:"id"`
	State    string `json:"state"`
	Body     string `json:"body"`
	HTMLURL  string `json:"html_url"`
	CommitID string `json:"commit_id"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
}

type githubReviewCommentPayload struct {
	ID int64 `json:"id"`
}

const githubReviewThreadsQuery = `
query($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $after) {
        pageInfo {
          hasNextPage
          endCursor
        }
        nodes {
          isResolved
          isOutdated
          path
          line
          comments(first: 100) {
            nodes {
              databaseId
              body
              path
              line
              originalLine
              url
              commit {
                oid
              }
              originalCommit {
                oid
              }
            }
          }
        }
      }
    }
  }
}`

type githubReviewThreadsGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type githubReviewThreadsGraphQLResponse struct {
	Data   githubReviewThreadsGraphQLData    `json:"data"`
	Errors []githubReviewThreadsGraphQLError `json:"errors"`
}

type githubReviewThreadsGraphQLError struct {
	Message string `json:"message"`
}

type githubReviewThreadsGraphQLData struct {
	Repository struct {
		PullRequest struct {
			ReviewThreads struct {
				Nodes    []githubReviewThreadPayload `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"reviewThreads"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

type githubReviewThreadPayload struct {
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Comments   struct {
		Nodes []githubThreadCommentPayload `json:"nodes"`
	} `json:"comments"`
}

type githubThreadCommentPayload struct {
	DatabaseID     int64  `json:"databaseId"`
	Body           string `json:"body"`
	Path           string `json:"path"`
	Line           int    `json:"line"`
	OriginalLine   int    `json:"originalLine"`
	URL            string `json:"url"`
	Commit         oidRef `json:"commit"`
	OriginalCommit oidRef `json:"originalCommit"`
}

type oidRef struct {
	OID string `json:"oid"`
}

type githubCheckRunsPayload struct {
	CheckRuns []githubCheckRunPayload `json:"check_runs"`
}

type githubCheckRunPayload struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	DetailsURL string `json:"details_url"`
	HeadSHA    string `json:"head_sha"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
}

type githubCombinedStatusPayload struct {
	SHA      string                `json:"sha"`
	Statuses []githubStatusPayload `json:"statuses"`
}

type githubStatusPayload struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
	Context     string `json:"context"`
}

func failedGitHubConclusion(conclusion string) bool {
	switch conclusion {
	case "failure", "timed_out", "action_required", "cancelled":
		return true
	default:
		return false
	}
}

func reviewThreadFeedbackItems(repo string, pullNumber int, headSHA string, thread githubReviewThreadPayload) []workflow.ChangeFeedbackItem {
	outdated := thread.IsOutdated
	actionable := !thread.IsResolved && !outdated
	items := make([]workflow.ChangeFeedbackItem, 0, len(thread.Comments.Nodes))
	for _, comment := range thread.Comments.Nodes {
		line := comment.Line
		if line == 0 {
			line = comment.OriginalLine
		}
		if line == 0 {
			line = thread.Line
		}
		path := comment.Path
		if path == "" {
			path = thread.Path
		}
		items = append(items, workflow.ChangeFeedbackItem{
			ProviderRef: fmt.Sprintf("github://github.com/%s/pulls/%d/comments/%d", repo, pullNumber, comment.DatabaseID),
			Kind:        "review_comment",
			Actionable:  actionable,
			Summary:     pathLineSummary(path, line),
			Body:        comment.Body,
			Path:        path,
			Line:        line,
			CommitSHA:   nonEmptyOr(reviewThreadCommentCommitSHA(comment), headSHA),
			State:       reviewThreadState(thread),
			ProviderSnapshot: map[string]any{
				"id":         comment.DatabaseID,
				"path":       path,
				"line":       line,
				"outdated":   outdated,
				"resolved":   thread.IsResolved,
				"html_url":   comment.URL,
				"thread_api": "graphql",
			},
		})
	}
	return items
}

func reviewThreadCommentCommitSHA(comment githubThreadCommentPayload) string {
	if comment.Commit.OID != "" {
		return comment.Commit.OID
	}
	return comment.OriginalCommit.OID
}

func reviewThreadState(thread githubReviewThreadPayload) string {
	if thread.IsResolved {
		return "resolved"
	}
	if thread.IsOutdated {
		return "outdated"
	}
	return "unresolved"
}

func isReviewDecisionState(state string) bool {
	switch strings.ToUpper(state) {
	case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
		return true
	default:
		return false
	}
}

func nonEmptyOr(value string, alternate string) string {
	if strings.TrimSpace(value) == "" {
		return alternate
	}
	return value
}

func pathLineSummary(path string, line int) string {
	if path == "" {
		return "Review comment"
	}
	if line <= 0 {
		return path
	}
	return fmt.Sprintf("%s:%d", path, line)
}

func splitGitHubRepository(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GitHub repository %q", repo)
	}
	return parts[0], parts[1], nil
}

func emptyStringAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nextGitHubPagePath(linkHeader string) string {
	for _, link := range strings.Split(linkHeader, ",") {
		segments := strings.Split(link, ";")
		if len(segments) < 2 {
			continue
		}
		if !strings.Contains(segments[1], `rel="next"`) {
			continue
		}
		rawURL := strings.TrimSpace(segments[0])
		rawURL = strings.TrimPrefix(rawURL, "<")
		rawURL = strings.TrimSuffix(rawURL, ">")
		if _, err := url.Parse(rawURL); err != nil {
			return ""
		}
		return rawURL
	}
	return ""
}
