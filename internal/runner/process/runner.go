package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

const (
	logSegmentBytes = 64 * 1024
	previewBytes    = 4 * 1024
)

// Runner executes AgentAdapter command plans as local OS processes.
type Runner struct{}

// RunAgentCommand starts the process, captures stdout/stderr log files, and returns segment indexes.
func (Runner) RunAgentCommand(ctx context.Context, plan workflow.AgentCommandPlan) (workflow.AgentCommandRunResult, error) {
	if err := os.MkdirAll(filepath.Dir(plan.StdoutPath), 0o755); err != nil {
		return workflow.AgentCommandRunResult{}, fmt.Errorf("create stdout log directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(plan.StderrPath), 0o755); err != nil {
		return workflow.AgentCommandRunResult{}, fmt.Errorf("create stderr log directory: %w", err)
	}

	cmd := exec.CommandContext(ctx, plan.Executable, plan.Args...)
	cmd.Dir = plan.WorkingDirectory
	cmd.Env = plan.Env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return workflow.AgentCommandRunResult{}, fmt.Errorf("open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return workflow.AgentCommandRunResult{}, fmt.Errorf("open stderr pipe: %w", err)
	}

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return workflow.AgentCommandRunResult{ProcessStarted: false}, err
	}

	capture := newLogCapture(plan)
	var wg sync.WaitGroup
	var stdoutErr error
	var stderrErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdoutErr = capture.captureStream("stdout", "logs/stdout.log", plan.StdoutPath, stdout)
	}()
	go func() {
		defer wg.Done()
		stderrErr = capture.captureStream("stderr", "logs/stderr.log", plan.StderrPath, stderr)
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	result := workflow.AgentCommandRunResult{
		ExitCode:       exitCode(waitErr),
		Duration:       time.Since(startedAt),
		StdoutBytes:    capture.stdoutBytes,
		StderrBytes:    capture.stderrBytes,
		LogSegments:    capture.sortedSegments(),
		ProcessStarted: true,
	}
	if stdoutErr != nil {
		return result, stdoutErr
	}
	if stderrErr != nil {
		return result, stderrErr
	}
	return result, waitErr
}

type logCapture struct {
	mu           sync.Mutex
	nextSequence int64
	stdoutBytes  int64
	stderrBytes  int64
	segments     []workflow.LogSegmentPlan
	redactValues []string
}

func newLogCapture(plan workflow.AgentCommandPlan) *logCapture {
	return &logCapture{
		nextSequence: 1,
		redactValues: cleanRedactValues(plan.RedactValues),
	}
}

func (capture *logCapture) captureStream(stream string, artifactPath string, filePath string, reader io.Reader) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("create %s log file: %w", stream, err)
	}
	defer file.Close()

	buffer := make([]byte, logSegmentBytes)
	redactor := newStreamRedactor(capture.redactValues)
	var offset int64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			var writeErr error
			offset, writeErr = capture.writeRedactedChunk(file, stream, artifactPath, offset, redactor.push(buffer[:n], false))
			if writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(readErr, io.EOF) {
			var writeErr error
			offset, writeErr = capture.writeRedactedChunk(file, stream, artifactPath, offset, redactor.flush())
			if writeErr != nil {
				return writeErr
			}
			break
		}
		if readErr != nil {
			return fmt.Errorf("read %s pipe: %w", stream, readErr)
		}
	}
	capture.mu.Lock()
	if stream == "stdout" {
		capture.stdoutBytes = offset
	} else {
		capture.stderrBytes = offset
	}
	capture.mu.Unlock()
	return nil
}

func (capture *logCapture) writeRedactedChunk(file *os.File, stream string, artifactPath string, offset int64, chunk []byte) (int64, error) {
	if len(chunk) == 0 {
		return offset, nil
	}
	written, writeErr := file.Write(chunk)
	if writeErr != nil {
		return offset, fmt.Errorf("write %s log file: %w", stream, writeErr)
	}
	if written != len(chunk) {
		return offset, fmt.Errorf("write %s log file: short write", stream)
	}
	capture.addSegment(stream, artifactPath, offset, offset+int64(written), preview(chunk))
	return offset + int64(written), nil
}

func (capture *logCapture) addSegment(stream string, artifactPath string, start int64, end int64, preview string) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	sequence := capture.nextSequence
	capture.nextSequence++
	capture.segments = append(capture.segments, workflow.LogSegmentPlan{
		Stream:       stream,
		Sequence:     sequence,
		ByteStart:    start,
		ByteEnd:      end,
		Preview:      preview,
		ArtifactPath: artifactPath,
	})
}

type streamRedactor struct {
	pending string
	secrets []string
}

func newStreamRedactor(redactValues []string) *streamRedactor {
	return &streamRedactor{
		secrets: redactValues,
	}
}

func (redactor *streamRedactor) push(chunk []byte, final bool) []byte {
	if len(redactor.secrets) == 0 {
		return chunk
	}
	redactor.pending += string(chunk)
	return []byte(redactor.drain(final))
}

func (redactor *streamRedactor) flush() []byte {
	return redactor.push(nil, true)
}

func (redactor *streamRedactor) drain(final bool) string {
	var output string
	for redactor.pending != "" {
		if secret, ok := redactor.secretPrefix(); ok {
			output += "[REDACTED]"
			redactor.pending = redactor.pending[len(secret):]
			continue
		}
		if !final && redactor.couldBecomeSecret() {
			break
		}
		output += redactor.pending[:1]
		redactor.pending = redactor.pending[1:]
	}
	return output
}

func (redactor *streamRedactor) secretPrefix() (string, bool) {
	for _, secret := range redactor.secrets {
		if len(redactor.pending) >= len(secret) && redactor.pending[:len(secret)] == secret {
			return secret, true
		}
	}
	return "", false
}

func (redactor *streamRedactor) couldBecomeSecret() bool {
	for _, secret := range redactor.secrets {
		if len(redactor.pending) < len(secret) && secret[:len(redactor.pending)] == redactor.pending {
			return true
		}
	}
	return false
}

func cleanRedactValues(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	sort.Slice(cleaned, func(i int, j int) bool {
		return len(cleaned[i]) > len(cleaned[j])
	})
	return cleaned
}

func (capture *logCapture) sortedSegments() []workflow.LogSegmentPlan {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	segments := append([]workflow.LogSegmentPlan(nil), capture.segments...)
	sort.Slice(segments, func(i int, j int) bool {
		return segments[i].Sequence < segments[j].Sequence
	})
	return segments
}

func preview(chunk []byte) string {
	if len(chunk) <= previewBytes {
		return string(chunk)
	}
	return string(chunk[:previewBytes])
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
