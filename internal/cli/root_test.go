package cli

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func executeForTest(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := NewRootCommand(Options{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	cmd.SetArgs(args)

	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestRootHelpShowsSkeletonCommands(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "--help")
	if err != nil {
		t.Fatalf("expected help to succeed: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}

	for _, want := range []string{
		"ForgeLane is an agentic software delivery control plane.",
		"init",
		"version",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected help output to contain %q, got:\n%s", want, stdout)
		}
	}

	if strings.Contains(stdout, "completion") {
		t.Fatalf("expected help output not to expose completion command, got:\n%s", stdout)
	}
}

func TestVersionCommandShowsDevelopmentDefaults(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "version")
	if err != nil {
		t.Fatalf("expected version command to succeed: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}

	for _, want := range []string{
		"Version: dev",
		"Commit: unknown",
		"Date: unknown",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected version output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRootVersionFlagUsesCobraVersionWiring(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "--version")
	if err != nil {
		t.Fatalf("expected --version to succeed: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "forgelane version dev") {
		t.Fatalf("expected cobra version output, got:\n%s", stdout)
	}
}

func TestUnknownCommandWritesErrorToStderr(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "definitely-not-a-command")
	if err == nil {
		t.Fatal("expected unknown command to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, `unknown command "definitely-not-a-command" for "forgelane"`) {
		t.Fatalf("expected unknown command error on stderr, got:\n%s", stderr)
	}
}

func TestInitWithRepoURLPersistsNormalizedGitHubForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitWithGitHubRepoShorthandPersistsSameForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github", "--repo", "owner/repo")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitInfersGitHubForgeProjectFromOriginRemote(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitAcceptsSupportedGitHubRemoteURLForms(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
	}{
		{name: "https", repoURL: "https://github.com/owner/repo"},
		{name: "https git suffix", repoURL: "https://github.com/owner/repo.git"},
		{name: "https query and fragment ignored", repoURL: "https://github.com/owner/repo?tab=readme#readme"},
		{name: "ssh scp", repoURL: "git@github.com:owner/repo.git"},
		{name: "ssh url", repoURL: "ssh://git@github.com/owner/repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			homeDir := t.TempDir()
			withWorkingDir(t, workingDir)
			withHomeDir(t, homeDir)

			_, stderr, err := executeForTest(t, "init", "--repo-url", tt.repoURL)
			if err != nil {
				t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
			}
			if stderr != "" {
				t.Fatalf("expected no stderr, got %q", stderr)
			}

			assertNoRepoLocalConfig(t, workingDir)
			assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
		})
	}
}

func TestInitRejectsInvalidInputsWithClearErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "unsupported provider",
			args:    []string{"init", "--provider", "gitlab", "--repo", "owner/repo"},
			wantErr: `unsupported provider "gitlab"`,
		},
		{
			name:    "invalid repo ref",
			args:    []string{"init", "--provider", "github", "--repo", "owner"},
			wantErr: `invalid GitHub repository ref "owner"`,
		},
		{
			name:    "repo ref with owner whitespace",
			args:    []string{"init", "--provider", "github", "--repo", "bad owner/repo"},
			wantErr: `invalid GitHub repository ref "bad owner/repo"`,
		},
		{
			name:    "repo ref with repository whitespace",
			args:    []string{"init", "--provider", "github", "--repo", "owner/bad repo"},
			wantErr: `invalid GitHub repository ref "owner/bad repo"`,
		},
		{
			name:    "repo ref with dot segment",
			args:    []string{"init", "--provider", "github", "--repo", "owner/."},
			wantErr: `invalid GitHub repository ref "owner/."`,
		},
		{
			name:    "unsupported remote url",
			args:    []string{"init", "--repo-url", "https://gitlab.com/owner/repo"},
			wantErr: `unsupported GitHub repository URL "https://gitlab.com/owner/repo"`,
		},
		{
			name:    "branch webpage url",
			args:    []string{"init", "--repo-url", "https://github.com/owner/repo/tree/main"},
			wantErr: `invalid GitHub repository URL "https://github.com/owner/repo/tree/main"`,
		},
		{
			name:    "ambiguous shorthand in repo url",
			args:    []string{"init", "--repo-url", "owner/repo"},
			wantErr: `unsupported GitHub repository URL "owner/repo"`,
		},
		{
			name:    "missing origin asks for repo url",
			args:    []string{"init", "--provider", "github"},
			wantErr: "missing or unsupported origin remote; pass --repo-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withWorkingDir(t, t.TempDir())
			withHomeDir(t, t.TempDir())

			stdout, stderr, err := executeForTest(t, tt.args...)
			if err == nil {
				t.Fatal("expected init to fail")
			}
			if stdout != "" {
				t.Fatalf("expected no stdout, got %q", stdout)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Fatalf("expected stderr to contain %q, got:\n%s", tt.wantErr, stderr)
			}
		})
	}
}

func TestInitInferenceInspectsOnlyOrigin(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://gitlab.com/owner/repo")
	runGit(t, workingDir, "remote", "add", "upstream", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, t.TempDir())

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github")
	if err == nil {
		t.Fatal("expected init to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, `unsupported GitHub repository URL "https://gitlab.com/owner/repo"`) {
		t.Fatalf("expected origin-only failure, got:\n%s", stderr)
	}
}

func TestInitExplicitRepositoryWinsOverInferredOrigin(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/origin/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github", "--repo", "explicit/repo")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/explicit/repo") {
		t.Fatalf("expected init output to describe explicit ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/explicit/repo"})
}

func TestInitDoesNotInferOriginWithoutProvider(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, t.TempDir())

	stdout, stderr, err := executeForTest(t, "init")
	if err == nil {
		t.Fatal("expected init to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "missing repository; pass --repo-url") {
		t.Fatalf("expected missing repository error, got:\n%s", stderr)
	}
}

func TestInitIsIdempotentForMatchingForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo"); err != nil {
		t.Fatalf("expected first init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("expected matching init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitPersistsMultipleForgeProjectsInGlobalState(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo"); err != nil {
		t.Fatalf("expected first init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/other/repo")
	if err != nil {
		t.Fatalf("expected second ForgeProject init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/other/repo") {
		t.Fatalf("expected second ForgeProject init output, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{
		"github://github.com/owner/repo",
		"github://github.com/other/repo",
	})
}

func withWorkingDir(t *testing.T, workingDir string) {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current working directory: %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

func runGit(t *testing.T, workingDir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = workingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func withHomeDir(t *testing.T, homeDir string) {
	t.Helper()

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
}

func assertNoRepoLocalConfig(t *testing.T, workingDir string) {
	t.Helper()

	configPath := filepath.Join(workingDir, ".forgelane", "repository.json")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected no repo-local repository config at %s, stat err: %v", configPath, err)
	}
}

func assertForgeProjects(t *testing.T, homeDir string, wantRefs []string) {
	t.Helper()
	wantRefs = slices.Clone(wantRefs)
	slices.Sort(wantRefs)

	dbPath := filepath.Join(homeDir, ".forgelane", "forgelane.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open ForgeLane database: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT provider_ref FROM forge_projects ORDER BY provider_ref")
	if err != nil {
		t.Fatalf("query ForgeProjects: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var providerRef string
		if err := rows.Scan(&providerRef); err != nil {
			t.Fatalf("scan ForgeProject: %v", err)
		}
		got = append(got, providerRef)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ForgeProjects: %v", err)
	}

	if strings.Join(got, "\n") != strings.Join(wantRefs, "\n") {
		t.Fatalf("unexpected ForgeProjects:\n got: %q\nwant: %q", got, wantRefs)
	}
}
