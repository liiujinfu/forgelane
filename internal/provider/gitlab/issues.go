package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/liiujinfu/forgelane/internal/workitems"
)

const defaultAPIBaseURL = "https://gitlab.com/api/v4"

// Options configures GitLab provider clients.
type Options struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// IssueProvider reads GitLab issues as WorkItem snapshots.
type IssueProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewIssueProvider creates a GitLab issue provider.
func NewIssueProvider(options Options) *IssueProvider {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	token := options.Token
	if token == "" {
		token = os.Getenv("FORGELANE_GITLAB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &IssueProvider{
		baseURL: baseURL,
		token:   token,
		client:  client,
	}
}

// GetIssue reads one GitLab issue snapshot.
func (provider *IssueProvider) GetIssue(ctx context.Context, ref workitems.ProviderRef) (workitems.ProviderIssue, error) {
	endpoint := fmt.Sprintf(
		"%s/projects/%s/issues/%d",
		provider.apiBaseURL(ref.ProviderHost),
		url.PathEscape(ref.RepositoryPath),
		ref.IssueNumber,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("create GitLab issue request: %w", err)
	}
	if provider.token != "" {
		request.Header.Set("PRIVATE-TOKEN", provider.token)
	}

	response, err := provider.client.Do(request)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("GitLab provider failure reading issue %s: %w", ref.String(), err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return workitems.ProviderIssue{}, workitems.NotFoundError{ProviderRef: ref.String()}
	case http.StatusUnauthorized, http.StatusForbidden:
		return workitems.ProviderIssue{}, workitems.AuthError{ProviderRef: ref.String()}
	default:
		return workitems.ProviderIssue{}, workitems.ProviderError{
			ProviderRef: ref.String(),
			StatusCode:  response.StatusCode,
		}
	}

	var payload issuePayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("decode GitLab issue %s: %w", ref.String(), err)
	}
	updatedAt, err := time.Parse(time.RFC3339, payload.UpdatedAt)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("decode GitLab issue updated_at for %s: %w", ref.String(), err)
	}

	return workitems.ProviderIssue{
		ProviderRef:         ref.String(),
		RepositoryRef:       ref.RepositoryRef(),
		Provider:            ref.Provider,
		ProviderIssueNumber: payload.IID,
		Title:               payload.Title,
		Body:                payload.Description,
		Status:              normalizeGitLabIssueState(payload.State),
		RawStatus:           payload.State,
		URL:                 payload.WebURL,
		ProviderUpdatedAt:   updatedAt,
	}, nil
}

func (provider *IssueProvider) apiBaseURL(providerHost string) string {
	if provider.baseURL != "" {
		return provider.baseURL
	}
	if providerHost == "" {
		return defaultAPIBaseURL
	}
	return "https://" + providerHost + "/api/v4"
}

type issuePayload struct {
	IID         int    `json:"iid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	WebURL      string `json:"web_url"`
	UpdatedAt   string `json:"updated_at"`
}

func normalizeGitLabIssueState(state string) string {
	switch state {
	case "opened", "open":
		return "open"
	case "closed":
		return "closed"
	default:
		return "unknown"
	}
}
