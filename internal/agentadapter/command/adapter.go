package command

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

// Adapter resolves generic command AgentAdapter presets into process plans.
type Adapter struct {
	Secrets SecretStore
}

// SecretStore resolves ForgeLane-managed secret material by id.
type SecretStore interface {
	ResolveSecret(secretID string) (string, bool)
}

// EnvSecretStore is the v0 local self-hosted secret source.
type EnvSecretStore struct{}

// ResolveSecret reads allowlisted local secrets from the ForgeLane process environment.
func (EnvSecretStore) ResolveSecret(secretID string) (string, bool) {
	switch secretID {
	case "env:OPENAI_API_KEY":
		return os.LookupEnv("OPENAI_API_KEY")
	default:
		return "", false
	}
}

// PlanAgentCommand converts a RunSpec command adapter config into a concrete command.
func (adapter Adapter) PlanAgentCommand(input workflow.AgentCommandPlanInput) (workflow.AgentCommandPlan, error) {
	var spec runSpec
	if err := json.Unmarshal([]byte(input.RunSpecJSON), &spec); err != nil {
		return workflow.AgentCommandPlan{}, fmt.Errorf("decode RunSpec AgentAdapter config: %w", err)
	}
	if spec.AgentAdapter.Kind != "command" {
		return workflow.AgentCommandPlan{}, fmt.Errorf("unsupported AgentAdapter kind %q", spec.AgentAdapter.Kind)
	}
	if spec.AgentAdapter.EnvPolicy != "scrubbed" {
		return workflow.AgentCommandPlan{}, fmt.Errorf("unsupported AgentAdapter env policy %q", spec.AgentAdapter.EnvPolicy)
	}
	plan := workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", harmlessEchoScript},
		WorkingDirectory: input.Workspace.Paths.Repo,
		Env:              scrubbedEnv(input),
		StdoutPath:       filepath.Join(input.Workspace.Paths.Logs, "stdout.log"),
		StderrPath:       filepath.Join(input.Workspace.Paths.Logs, "stderr.log"),
	}
	switch spec.AgentAdapter.Preset {
	case "harmless-echo":
		return plan, nil
	case "codex":
		env, redactions, err := adapter.codexEnv(spec, input)
		if err != nil {
			return workflow.AgentCommandPlan{}, err
		}
		plan.Executable = "codex"
		plan.Args = []string{
			"exec",
			"--cd", input.Workspace.Paths.Repo,
			"--sandbox", "workspace-write",
			"Print exactly: ForgeLane Codex smoke OK. Do not modify files.",
		}
		plan.Env = env
		plan.RedactValues = redactions
		return plan, nil
	default:
		return workflow.AgentCommandPlan{}, fmt.Errorf("unsupported command AgentAdapter preset %q", spec.AgentAdapter.Preset)
	}
}

func (adapter Adapter) secretStore() SecretStore {
	if adapter.Secrets != nil {
		return adapter.Secrets
	}
	return EnvSecretStore{}
}

type runSpec struct {
	AgentAdapter struct {
		Kind             string            `json:"kind"`
		Preset           string            `json:"preset"`
		EnvPolicy        string            `json:"env_policy"`
		CredentialGrants []credentialGrant `json:"credential_grants"`
	} `json:"agent_adapter"`
}

type credentialGrant struct {
	Kind     string `json:"kind"`
	SecretID string `json:"secret_id"`
	Env      string `json:"env"`
}

const harmlessEchoScript = `printf 'forgelane harmless stdout\n'
printf 'forgelane harmless stderr\n' >&2
printf 'cwd=%s\n' "$PWD"
if [ -n "${FORGELANE_GITHUB_TOKEN:-}" ] || [ -n "${GITHUB_TOKEN:-}" ] || [ -n "${GH_TOKEN:-}" ] || [ -n "${FORGELANE_GITLAB_TOKEN:-}" ] || [ -n "${GITLAB_TOKEN:-}" ]; then
  printf 'provider-token=present\n'
else
  printf 'provider-token=absent\n'
fi
`

func scrubbedEnv(input workflow.AgentCommandPlanInput) []string {
	env := make([]string, 0, 12)
	for _, key := range []string{"PATH", "LANG", "LC_ALL"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env,
		"HOME="+input.Workspace.Paths.Tmp,
		"TMPDIR="+input.Workspace.Paths.Tmp,
		"TEMP="+input.Workspace.Paths.Tmp,
		"TMP="+input.Workspace.Paths.Tmp,
		fmt.Sprintf("FORGELANE_RUN_ID=%d", input.AgentRunID),
		"FORGELANE_WORKSPACE="+input.Workspace.Paths.Root,
		"FORGELANE_REPO="+input.Workspace.Paths.Repo,
	)
	return env
}

func (adapter Adapter) codexEnv(spec runSpec, input workflow.AgentCommandPlanInput) ([]string, []string, error) {
	env := scrubbedEnv(input)
	for _, grant := range spec.AgentAdapter.CredentialGrants {
		if grant.Kind != "openai_api_key" {
			continue
		}
		if grant.Env != "OPENAI_API_KEY" {
			return nil, nil, fmt.Errorf("unsupported openai_api_key credential grant env %q", grant.Env)
		}
		if grant.SecretID != "env:OPENAI_API_KEY" {
			return nil, nil, fmt.Errorf("unsupported openai_api_key credential grant secret %q", grant.SecretID)
		}
		value, ok := adapter.secretStore().ResolveSecret(grant.SecretID)
		if !ok || value == "" {
			return nil, nil, fmt.Errorf("OPENAI_API_KEY credential grant %q is not available", grant.SecretID)
		}
		return append(env, "OPENAI_API_KEY="+value), []string{value}, nil
	}
	return nil, nil, fmt.Errorf("codex preset requires an OPENAI_API_KEY credential grant")
}
