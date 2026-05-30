package process_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
