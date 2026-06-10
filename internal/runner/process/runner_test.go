package process_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	processrunner "github.com/liiujinfu/forgelane/internal/runner/process"
	"github.com/liiujinfu/forgelane/internal/workflow"
)

func TestRunnerRedactsSecretsFromLogFilesAndPreviews(t *testing.T) {
	workspace := t.TempDir()
	secret := "sk-test-secret"
	plan := workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", "printf '%s\\n' \"$OPENAI_API_KEY\"; printf 'stderr:%s\\n' \"$OPENAI_API_KEY\" >&2"},
		WorkingDirectory: workspace,
		Env:              []string{"PATH=" + os.Getenv("PATH"), "OPENAI_API_KEY=" + secret},
		StdoutPath:       filepath.Join(workspace, "logs", "stdout.log"),
		StderrPath:       filepath.Join(workspace, "logs", "stderr.log"),
		RedactValues:     []string{secret},
	}

	result, err := (processrunner.Runner{}).RunAgentCommand(context.Background(), plan)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	for _, path := range []string{plan.StdoutPath, plan.StderrPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log file %s: %v", path, err)
		}
		if strings.Contains(string(content), secret) {
			t.Fatalf("expected log file %s to redact secret, got:\n%s", path, string(content))
		}
		if !strings.Contains(string(content), "[REDACTED]") {
			t.Fatalf("expected log file %s to contain redaction marker, got:\n%s", path, string(content))
		}
	}
	for _, segment := range result.LogSegments {
		if strings.Contains(segment.Preview, secret) {
			t.Fatalf("expected log preview to redact secret, got %#v", segment)
		}
	}
}

func TestRunnerReportsNonZeroExitWhilePreservingLogs(t *testing.T) {
	workspace := t.TempDir()
	plan := workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", "printf 'before failure\\n'; exit 7"},
		WorkingDirectory: workspace,
		Env:              []string{"PATH=" + os.Getenv("PATH")},
		StdoutPath:       filepath.Join(workspace, "logs", "stdout.log"),
		StderrPath:       filepath.Join(workspace, "logs", "stderr.log"),
	}

	result, err := (processrunner.Runner{}).RunAgentCommand(context.Background(), plan)
	if err == nil {
		t.Fatal("expected non-zero command to return an error")
	}
	if result.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", result.ExitCode)
	}
	if !result.ProcessStarted {
		t.Fatal("expected process to have started")
	}

	content, err := os.ReadFile(plan.StdoutPath)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if string(content) != "before failure\n" {
		t.Fatalf("unexpected stdout log: %q", string(content))
	}
}

func TestRunnerDoesNotAttachInteractiveStdin(t *testing.T) {
	workspace := t.TempDir()
	plan := workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", "if read -r line; then printf 'stdin=%s\\n' \"$line\"; else printf 'stdin=closed\\n'; fi"},
		WorkingDirectory: workspace,
		Env:              []string{"PATH=" + os.Getenv("PATH")},
		StdoutPath:       filepath.Join(workspace, "logs", "stdout.log"),
		StderrPath:       filepath.Join(workspace, "logs", "stderr.log"),
	}

	result, err := (processrunner.Runner{}).RunAgentCommand(context.Background(), plan)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected stdin check to exit cleanly, got %d", result.ExitCode)
	}
	content, err := os.ReadFile(plan.StdoutPath)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if string(content) != "stdin=closed\n" {
		t.Fatalf("expected AgentAdapter process stdin to be closed, got %q", string(content))
	}
}

func TestRunnerStopsCommandWhenContextTimesOut(t *testing.T) {
	workspace := t.TempDir()
	plan := workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", "sleep 1"},
		WorkingDirectory: workspace,
		Env:              []string{"PATH=" + os.Getenv("PATH")},
		StdoutPath:       filepath.Join(workspace, "logs", "stdout.log"),
		StderrPath:       filepath.Join(workspace, "logs", "stderr.log"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := (processrunner.Runner{}).RunAgentCommand(ctx, plan)
	if err == nil {
		t.Fatal("expected timed-out command to return an error")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected context deadline to be exceeded, got %v", ctx.Err())
	}
	if !result.ProcessStarted {
		t.Fatal("expected process to have started")
	}
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero timeout exit code, got %d", result.ExitCode)
	}
}
