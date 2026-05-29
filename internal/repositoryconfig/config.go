// Package repositoryconfig manages ForgeLane-owned local repository defaults.
package repositoryconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const configRelativePath = ".forgelane/repository.json"

// ForgeProject identifies the default provider-backed project for local work.
type ForgeProject struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Path     string `json:"path"`
}

type repositoryConfig struct {
	ForgeProject ForgeProject `json:"forgeProject"`
}

// InitOptions configures repository default initialization.
type InitOptions struct {
	WorkingDir string
	RepoURL    string
	Provider   string
	Repo       string
	Force      bool
}

// Configure writes the local ForgeProject repository config.
func Configure(options InitOptions) (ForgeProject, error) {
	workingDir := options.WorkingDir
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return ForgeProject{}, fmt.Errorf("resolve working directory: %w", err)
		}
	}

	options.WorkingDir = workingDir
	forgeProject, err := forgeProjectFromOptions(options)
	if err != nil {
		return ForgeProject{}, err
	}

	configPath := filepath.Join(workingDir, configRelativePath)
	if err := verifyExistingConfig(configPath, forgeProject, options.Force); err != nil {
		return ForgeProject{}, err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return ForgeProject{}, fmt.Errorf("create repository config directory: %w", err)
	}

	encoded, err := json.MarshalIndent(repositoryConfig{ForgeProject: forgeProject}, "", "  ")
	if err != nil {
		return ForgeProject{}, fmt.Errorf("encode repository config: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.WriteFile(configPath, encoded, 0o644); err != nil {
		return ForgeProject{}, fmt.Errorf("write repository config: %w", err)
	}

	return forgeProject, nil
}

// ForgeProjectRef returns the canonical project reference used in CLI output.
func ForgeProjectRef(forgeProject ForgeProject) string {
	baseURL, err := url.Parse(forgeProject.BaseURL)
	if err != nil || baseURL.Host == "" {
		return forgeProject.Provider + "://" + forgeProject.Path
	}
	return forgeProject.Provider + "://" + baseURL.Host + "/" + forgeProject.Path
}

func verifyExistingConfig(configPath string, requested ForgeProject, force bool) error {
	existingBytes, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read existing repository config: %w", err)
	}

	var existing repositoryConfig
	if err := json.Unmarshal(existingBytes, &existing); err != nil {
		return fmt.Errorf("parse existing repository config: %w", err)
	}
	if existing.ForgeProject == requested {
		return nil
	}
	if force {
		return nil
	}

	return fmt.Errorf(
		"repository config already points at %s; pass --force to replace it",
		ForgeProjectRef(existing.ForgeProject),
	)
}

func forgeProjectFromOptions(options InitOptions) (ForgeProject, error) {
	if options.RepoURL != "" && options.Repo != "" {
		return ForgeProject{}, fmt.Errorf("pass only one of --repo-url or --repo")
	}

	provider := options.Provider
	if provider == "" && options.RepoURL != "" {
		provider = "github"
	}
	if provider == "" && options.RepoURL == "" && options.Repo == "" {
		return ForgeProject{}, fmt.Errorf("missing repository; pass --repo-url")
	}
	if provider != "github" {
		return ForgeProject{}, fmt.Errorf("unsupported provider %q", provider)
	}

	var repoPath string
	var err error
	switch {
	case options.RepoURL != "":
		repoPath, err = parseGitHubURL(options.RepoURL)
	case options.Repo != "":
		repoPath, err = parseGitHubRepo(options.Repo)
	default:
		var originURL string
		originURL, err = originRemoteURL(options.WorkingDir)
		if err != nil {
			return ForgeProject{}, err
		}
		repoPath, err = parseGitHubURL(originURL)
	}
	if err != nil {
		return ForgeProject{}, err
	}

	return ForgeProject{
		Provider: "github",
		BaseURL:  "https://github.com",
		Path:     repoPath,
	}, nil
}

func parseGitHubURL(raw string) (string, error) {
	if repoPath, ok := parseGitHubSCPRemote(raw); ok {
		return repoPath, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid GitHub repository URL %q", raw)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return "", fmt.Errorf("unsupported GitHub repository URL %q", raw)
	}
	if parsed.Host != "github.com" {
		return "", fmt.Errorf("unsupported GitHub repository URL %q", raw)
	}
	if parsed.Scheme == "ssh" && parsed.User.Username() != "git" {
		return "", fmt.Errorf("unsupported GitHub repository URL %q", raw)
	}

	repoPath := strings.Trim(path.Clean(parsed.Path), "/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	if !validOwnerRepo(repoPath) {
		return "", fmt.Errorf("invalid GitHub repository URL %q", raw)
	}
	return repoPath, nil
}

func parseGitHubSCPRemote(raw string) (string, bool) {
	const prefix = "git@github.com:"
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	repoPath := strings.TrimSuffix(strings.TrimPrefix(raw, prefix), ".git")
	return repoPath, validOwnerRepo(repoPath)
}

func parseGitHubRepo(raw string) (string, error) {
	repoPath := strings.TrimSpace(raw)
	if !validOwnerRepo(repoPath) {
		return "", fmt.Errorf("invalid GitHub repository ref %q", raw)
	}
	return repoPath, nil
}

func validOwnerRepo(repoPath string) bool {
	parts := strings.Split(repoPath, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func originRemoteURL(workingDir string) (string, error) {
	cmd := exec.Command("git", "-C", workingDir, "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("missing or unsupported origin remote; pass --repo-url")
	}

	originURL := strings.TrimSpace(string(output))
	if originURL == "" {
		return "", fmt.Errorf("missing or unsupported origin remote; pass --repo-url")
	}
	return originURL, nil
}
