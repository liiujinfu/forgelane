// Package workflowcontract manages the repo-owned ForgeLane workflow contract.
package workflowcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FileName is the repo-root workflow contract path.
const FileName = "forgelane.workflow.json"

// ErrRepositoryRootNotFound means the working directory is not inside a Git repository.
var ErrRepositoryRootNotFound = errors.New("workflow contract repository root not found")

const (
	// RoleTrigger marks WorkItems that future watcher automation may consider.
	RoleTrigger = "trigger"
	// RoleReadyForAgent marks WorkItems ready for automated agent pickup.
	RoleReadyForAgent = "ready_for_agent"
	// RoleNeedsInfo marks WorkItems waiting on reporter or maintainer input.
	RoleNeedsInfo = "needs_info"
	// RoleReadyForHuman marks WorkItems requiring human implementation or decision.
	RoleReadyForHuman = "ready_for_human"
)

// Contract declares durable repository expectations for ForgeLane agent runs.
type Contract struct {
	Version         int                 `json:"version"`
	Agent           AgentRules          `json:"agent"`
	Tracker         TrackerRules        `json:"tracker"`
	Verification    VerificationRules   `json:"verification"`
	Approvals       ApprovalPolicyHints `json:"approvals"`
	AutomationNotes []string            `json:"automation_notes"`
}

// AgentRules declares default AgentAdapter behavior.
type AgentRules struct {
	DefaultPreset string `json:"default_preset"`
}

// TrackerRules maps ForgeLane semantic tracker roles to provider labels.
type TrackerRules struct {
	Labels map[string]string `json:"labels"`
}

// VerificationRules documents the evidence expected from agent runs.
type VerificationRules struct {
	TestCommand  string   `json:"test_command"`
	Evidence     []string `json:"evidence"`
	ManualReview []string `json:"manual_review"`
}

// ApprovalPolicyHints document privileged action expectations.
type ApprovalPolicyHints struct {
	ProviderMutations string `json:"provider_mutations"`
	PrivilegedActions string `json:"privileged_actions"`
}

// Default returns the conservative default workflow contract.
func Default() Contract {
	return Contract{
		Version: 1,
		Agent: AgentRules{
			DefaultPreset: "codex",
		},
		Tracker: TrackerRules{
			Labels: map[string]string{
				RoleTrigger:       "forgelane",
				RoleReadyForAgent: "ready-for-agent",
				RoleNeedsInfo:     "needs-info",
				RoleReadyForHuman: "ready-for-human",
			},
		},
		Verification: VerificationRules{
			TestCommand: "go test ./...",
			Evidence: []string{
				"relevant tests pass",
				"git diff --check passes",
			},
			ManualReview: []string{
				"diff summary",
				"verification evidence",
			},
		},
		Approvals: ApprovalPolicyHints{
			ProviderMutations: "Record an explicit ControlAction before mutating provider-owned branches, PRs, or MRs.",
			PrivilegedActions: "Route privileged or irreversible actions through an approval boundary before execution.",
		},
		AutomationNotes: []string{
			"Manual runs create/start do not require tracker label eligibility.",
			"Future watcher behavior is out of scope for this slice; when added, it should use tracker.labels.trigger and tracker.labels.ready_for_agent.",
		},
	}
}

// Path returns the workflow contract path for a working directory.
func Path(workingDir string) string {
	if workingDir == "" {
		workingDir = "."
	}
	return filepath.Join(workingDir, FileName)
}

// RepositoryRoot resolves the Git repository root that owns the workflow contract.
func RepositoryRoot(workingDir string) (string, error) {
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
	}
	cmd := exec.Command("git", "-C", workingDir, "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: run from a Git repository", ErrRepositoryRootNotFound)
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", fmt.Errorf("%w: empty Git repository root", ErrRepositoryRootNotFound)
	}
	return root, nil
}

// Exists reports whether the workflow contract exists.
func Exists(workingDir string) (bool, error) {
	_, err := os.Stat(Path(workingDir))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect workflow contract: %w", err)
}

// InitDefault creates the default workflow contract and refuses to overwrite.
func InitDefault(workingDir string) error {
	encoded, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode default workflow contract: %w", err)
	}
	encoded = append(encoded, '\n')

	file, err := os.OpenFile(Path(workingDir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("workflow contract %s already exists", FileName)
		}
		return fmt.Errorf("create workflow contract: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("write workflow contract: %w", err)
	}
	return nil
}

// Load reads the workflow contract from the working directory.
func Load(workingDir string) (Contract, error) {
	content, err := os.ReadFile(Path(workingDir))
	if err != nil {
		return Contract{}, err
	}
	var contract Contract
	if err := json.Unmarshal(content, &contract); err != nil {
		return Contract{}, fmt.Errorf("decode workflow contract %s: %w", FileName, err)
	}
	return contract, nil
}

// DefaultAgentPreset returns the contract default, falling back when missing.
func DefaultAgentPreset(workingDir string) (string, error) {
	contract, err := Load(workingDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default().Agent.DefaultPreset, nil
		}
		return "", err
	}
	if contract.Agent.DefaultPreset == "" {
		return Default().Agent.DefaultPreset, nil
	}
	return contract.Agent.DefaultPreset, nil
}
