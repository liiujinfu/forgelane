package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
	"github.com/liiujinfu/forgelane/internal/workflow"
)

// LocalWorkspacePreparer prepares a local Git checkout for one Workspace.
type LocalWorkspacePreparer struct{}

// PrepareWorkspace creates the workspace directories and prepares the repo checkout.
func (LocalWorkspacePreparer) PrepareWorkspace(preparation workflow.WorkspacePreparation) error {
	return prepareWorkspaceRepository(preparation.Paths, preparation.ExpectedRepositoryRef)
}

func prepareWorkspaceRepository(paths workflow.WorkspacePaths, expectedRepositoryRef string) error {
	for _, path := range []string{paths.Root, paths.Logs, paths.Artifacts, paths.Tmp} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create Workspace directory %s: %w", path, err)
		}
	}
	sourceProject, err := repositoryconfig.InferForgeProjectFromOrigin("")
	if err != nil {
		return fmt.Errorf("infer Workspace source repository: %w", err)
	}
	sourceRepositoryRef := repositoryconfig.ForgeProjectRef(sourceProject)
	if sourceRepositoryRef != expectedRepositoryRef {
		return fmt.Errorf("Workspace source repository %s does not match RunSpec repository %s", sourceRepositoryRef, expectedRepositoryRef)
	}
	if _, err := os.Stat(paths.Repo); err == nil {
		return fmt.Errorf("Workspace repository path already exists: %s", paths.Repo)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Workspace repository path %s: %w", paths.Repo, err)
	}

	cmd := exec.Command("git", "clone", "--no-hardlinks", ".", paths.Repo)
	output, err := cmd.CombinedOutput()
	if err != nil {
		writeWorkspacePrepareLog(paths, output)
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func writeWorkspacePrepareLog(paths workflow.WorkspacePaths, output []byte) {
	if len(output) == 0 {
		return
	}
	if err := os.MkdirAll(paths.Logs, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(paths.Logs, "checkout.log"), output, 0o644)
}
