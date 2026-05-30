package command_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liiujinfu/forgelane/internal/agentadapter/command"
	"github.com/liiujinfu/forgelane/internal/workflow"
)

func TestCodexPresetPlansSmokeCommandWithScrubbedEnvironment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "sensitive-provider-token")
	t.Setenv("GH_TOKEN", "sensitive-gh-token")
	t.Setenv("GITLAB_TOKEN", "sensitive-gitlab-token")
	t.Setenv("HOME", "/host-home")
	t.Setenv("CODEX_HOME", "/codex-home")

	spec := map[string]any{
		"agent_adapter": map[string]any{
			"kind":       "command",
			"preset":     "codex",
			"env_policy": "scrubbed",
			"credential_grants": []map[string]any{
				{
					"kind":      "openai_api_key",
					"secret_id": "env:OPENAI_API_KEY",
					"env":       "OPENAI_API_KEY",
				},
			},
		},
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encode RunSpec: %v", err)
	}

	plan, err := (command.Adapter{
		Secrets: fakeSecretStore{"env:OPENAI_API_KEY": "sk-test"},
	}).PlanAgentCommand(workflow.AgentCommandPlanInput{
		RunSpecJSON: string(specJSON),
		AgentRunID:  123,
		Workspace: workflow.Workspace{
			Paths: workflow.WorkspacePaths{
				Root: "/workspace/run-123",
				Repo: "/workspace/run-123/repo",
				Logs: "/workspace/run-123/logs",
				Tmp:  "/workspace/run-123/tmp",
			},
		},
	})
	if err != nil {
		t.Fatalf("plan codex preset: %v", err)
	}
	if plan.Executable != "codex" {
		t.Fatalf("expected codex executable, got %q", plan.Executable)
	}
	wantArgs := []string{"exec", "--cd", "/workspace/run-123/repo", "--sandbox", "workspace-write"}
	for _, want := range wantArgs {
		if !contains(plan.Args, want) {
			t.Fatalf("expected codex args to contain %q, got %#v", want, plan.Args)
		}
	}
	if strings.Join(plan.Env, "\n") == "" {
		t.Fatal("expected scrubbed environment")
	}
	if !contains(plan.Env, "HOME=/workspace/run-123/tmp") {
		t.Fatalf("expected HOME to be scoped to Workspace tmp, got env %#v", plan.Env)
	}
	if !contains(plan.Env, "OPENAI_API_KEY=sk-test") {
		t.Fatalf("expected OPENAI_API_KEY grant to be injected, got env %#v", plan.Env)
	}
	for _, value := range plan.Env {
		switch {
		case strings.HasPrefix(value, "GITHUB_TOKEN="):
			t.Fatalf("expected GITHUB_TOKEN to be scrubbed, got env %#v", plan.Env)
		case strings.HasPrefix(value, "GH_TOKEN="):
			t.Fatalf("expected GH_TOKEN to be scrubbed, got env %#v", plan.Env)
		case strings.HasPrefix(value, "GITLAB_TOKEN="):
			t.Fatalf("expected GITLAB_TOKEN to be scrubbed, got env %#v", plan.Env)
		case strings.HasPrefix(value, "CODEX_HOME="):
			t.Fatalf("expected CODEX_HOME not to be inherited, got env %#v", plan.Env)
		case value == "HOME=/host-home":
			t.Fatalf("expected host HOME not to be inherited, got env %#v", plan.Env)
		}
	}
}

func TestCodexPresetRequiresDeclaredOpenAIAPIKeyGrant(t *testing.T) {
	spec := map[string]any{
		"agent_adapter": map[string]any{
			"kind":       "command",
			"preset":     "codex",
			"env_policy": "scrubbed",
		},
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encode RunSpec: %v", err)
	}

	_, err = (command.Adapter{
		Secrets: fakeSecretStore{"env:OPENAI_API_KEY": "sk-test"},
	}).PlanAgentCommand(workflow.AgentCommandPlanInput{
		RunSpecJSON: string(specJSON),
		AgentRunID:  123,
		Workspace: workflow.Workspace{
			Paths: workflow.WorkspacePaths{
				Root: "/workspace/run-123",
				Repo: "/workspace/run-123/repo",
				Logs: "/workspace/run-123/logs",
				Tmp:  "/workspace/run-123/tmp",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY credential grant") {
		t.Fatalf("expected missing grant error, got %v", err)
	}
}

type fakeSecretStore map[string]string

func (store fakeSecretStore) ResolveSecret(secretID string) (string, bool) {
	value, ok := store[secretID]
	return value, ok
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
