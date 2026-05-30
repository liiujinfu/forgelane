// Package repositoryconfig manages ForgeLane-owned repository defaults.
package repositoryconfig

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
)

const stateRelativePath = ".forgelane/forgelane.db"

// ForgeProject identifies the default provider-backed project for local work.
type ForgeProject struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Path     string `json:"path"`
}

// InitOptions configures repository default initialization.
type InitOptions struct {
	WorkingDir string
	DBPath     string
	RepoURL    string
	Provider   string
	Repo       string
}

// Configure persists a ForgeProject in the instance-global state store.
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

	dbPath, err := StateDBPath(options.DBPath)
	if err != nil {
		return ForgeProject{}, err
	}

	instanceStore, err := store.Open(dbPath)
	if err != nil {
		return ForgeProject{}, err
	}
	defer instanceStore.Close()

	if err := instanceStore.Initialize(); err != nil {
		return ForgeProject{}, err
	}
	if err := instanceStore.UpsertForgeProject(store.ForgeProject{
		Provider:       forgeProject.Provider,
		ProviderHost:   forgeProjectHost(forgeProject),
		RepositoryPath: forgeProject.Path,
		ProviderRef:    ForgeProjectRef(forgeProject),
	}); err != nil {
		return ForgeProject{}, err
	}
	return forgeProject, nil
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

// InferForgeProjectFromOrigin returns the ForgeProject implied by a directory's origin remote.
func InferForgeProjectFromOrigin(workingDir string) (ForgeProject, error) {
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return ForgeProject{}, fmt.Errorf("resolve working directory: %w", err)
		}
	}
	originURL, err := originRemoteURL(workingDir)
	if err != nil {
		return ForgeProject{}, err
	}
	repoPath, err := parseGitHubURL(originURL)
	if err != nil {
		return ForgeProject{}, err
	}
	return ForgeProject{
		Provider: "github",
		BaseURL:  "https://github.com",
		Path:     repoPath,
	}, nil
}

// StateDBPath returns the ForgeLane instance-global SQLite path.
func StateDBPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve ForgeLane state directory: %w", err)
	}
	if homeDir == "" {
		return "", fmt.Errorf("resolve ForgeLane state directory: empty home directory")
	}
	return filepath.Join(homeDir, stateRelativePath), nil
}

// ForgeProjectRef returns the canonical project reference used in CLI output.
func ForgeProjectRef(forgeProject ForgeProject) string {
	baseURL, err := url.Parse(forgeProject.BaseURL)
	if err != nil || baseURL.Host == "" {
		return forgeProject.Provider + "://" + forgeProject.Path
	}
	return forgeProject.Provider + "://" + baseURL.Host + "/" + forgeProject.Path
}

func forgeProjectHost(forgeProject ForgeProject) string {
	baseURL, err := url.Parse(forgeProject.BaseURL)
	if err != nil || baseURL.Host == "" {
		return ""
	}
	return baseURL.Host
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
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
			return false
		}
		for _, char := range part {
			if char < 0x21 || char > 0x7e || strings.ContainsRune(`\/?#[]@!$&'()*+,;=`, char) {
				return false
			}
		}
	}
	return true
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
