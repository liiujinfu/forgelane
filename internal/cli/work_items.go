package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/liiujinfu/forgelane/internal/repositoryconfig"
	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workitems"
	"github.com/spf13/cobra"
)

func newWorkItemsCommand(stdout io.Writer, options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "work-items",
		Short: "Import and inspect provider-owned WorkItems.",
	}
	cmd.AddCommand(newWorkItemsImportCommand(stdout, options))
	cmd.AddCommand(newWorkItemsShowCommand(stdout))
	return cmd
}

func newWorkItemsImportCommand(stdout io.Writer, options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "import <provider-ref>",
		Short: "Import a provider-owned WorkItem snapshot.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instanceStore, err := openInitializedStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			ref, err := resolveWorkItemRef(args[0], instanceStore)
			if err != nil {
				return err
			}
			provider, err := workItemProviderForRef(options, ref)
			if err != nil {
				return err
			}
			issue, err := provider.GetIssue(cmd.Context(), ref)
			if err != nil {
				return err
			}
			issue = issue.Normalize(ref)

			result, err := instanceStore.ImportWorkItem(issue)
			if err != nil {
				return err
			}
			printImportedWorkItem(stdout, result)
			return nil
		},
	}
}

func newWorkItemsShowCommand(stdout io.Writer) *cobra.Command {
	var issueNumber string
	var localID string

	cmd := &cobra.Command{
		Use:   "show <provider-ref>",
		Short: "Show a cached WorkItem snapshot.",
		Args: func(_ *cobra.Command, args []string) error {
			flagCount := 0
			if issueNumber != "" {
				flagCount++
			}
			if localID != "" {
				flagCount++
			}
			if flagCount > 0 {
				if len(args) != 0 {
					return fmt.Errorf("pass either a ProviderRef argument, --issue, or --id")
				}
				if flagCount > 1 {
					return fmt.Errorf("pass only one of --issue or --id")
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("requires a ProviderRef argument or --issue")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			instanceStore, err := openReadOnlyStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			if localID != "" {
				id, err := strconv.ParseInt(localID, 10, 64)
				if err != nil || id <= 0 {
					return fmt.Errorf("invalid WorkItem id %q", localID)
				}
				workItem, err := instanceStore.GetWorkItemByID(id)
				if err != nil {
					return err
				}
				printWorkItem(stdout, workItem)
				return nil
			}

			input := issueNumber
			if input == "" {
				input = args[0]
			}
			ref, err := resolveWorkItemRef(input, instanceStore)
			if err != nil {
				return err
			}

			workItem, err := instanceStore.GetWorkItemByProviderRef(ref.String())
			if err != nil {
				return err
			}
			printWorkItem(stdout, workItem)
			return nil
		},
	}
	cmd.Flags().StringVar(&issueNumber, "issue", "", "Issue number in the current ForgeProject")
	cmd.Flags().StringVar(&localID, "id", "", "Local WorkItem id")
	return cmd
}

func resolveWorkItemRef(input string, instanceStore *store.Store) (workitems.ProviderRef, error) {
	if isPositiveInteger(input) {
		return resolveIssueNumber(input, instanceStore)
	}
	return workitems.ParseProviderRef(input)
}

func resolveIssueNumber(input string, instanceStore *store.Store) (workitems.ProviderRef, error) {
	issueNumber, err := strconv.Atoi(input)
	if err != nil || issueNumber <= 0 {
		return workitems.ProviderRef{}, fmt.Errorf("invalid issue number %q", input)
	}
	forgeProject, err := repositoryconfig.InferForgeProjectFromOrigin("")
	if err != nil {
		gitLabProject, gitLabErr := repositoryconfig.InferForgeProjectFromOriginForProvider("", "gitlab")
		if gitLabErr != nil {
			return workitems.ProviderRef{}, fmt.Errorf("%w; pass a full ProviderRef or run forgelane init", err)
		}
		ref, lookupErr := resolveIssueNumberFromForgeProject(issueNumber, gitLabProject, instanceStore)
		if lookupErr == nil {
			return ref, nil
		}
		return workitems.ProviderRef{}, fmt.Errorf("%w; pass a full ProviderRef or run forgelane init", err)
	}
	return resolveIssueNumberFromForgeProject(issueNumber, forgeProject, instanceStore)
}

func resolveIssueNumberFromForgeProject(issueNumber int, forgeProject repositoryconfig.ForgeProject, instanceStore *store.Store) (workitems.ProviderRef, error) {
	projectRef := repositoryconfig.ForgeProjectRef(forgeProject)
	persistedProject, err := instanceStore.GetForgeProjectByRef(projectRef)
	if err != nil {
		return workitems.ProviderRef{}, fmt.Errorf("%w; pass a full ProviderRef or run forgelane init", err)
	}
	return workitems.ProviderRef{
		Provider:       persistedProject.Provider,
		ProviderHost:   persistedProject.ProviderHost,
		RepositoryPath: persistedProject.RepositoryPath,
		IssueNumber:    issueNumber,
	}, nil
}

func isPositiveInteger(input string) bool {
	if strings.TrimSpace(input) != input || input == "" {
		return false
	}
	for _, char := range input {
		if char < '0' || char > '9' {
			return false
		}
	}
	return input != "0"
}

func openInitializedStore() (*store.Store, error) {
	dbPath, err := repositoryconfig.StateDBPath("")
	if err != nil {
		return nil, err
	}
	instanceStore, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := instanceStore.Initialize(); err != nil {
		instanceStore.Close()
		return nil, err
	}
	return instanceStore, nil
}

func openReadOnlyStore() (*store.Store, error) {
	dbPath, err := repositoryconfig.StateDBPath("")
	if err != nil {
		return nil, err
	}
	return store.OpenReadOnly(dbPath)
}

func printImportedWorkItem(stdout io.Writer, result store.WorkItemImportResult) {
	workItem := result.WorkItem
	fmt.Fprintf(stdout, "Imported WorkItem %s\n", workItem.ProviderRef)
	fmt.Fprintf(stdout, "Repository: %s\n", workItem.RepositoryRef)
	fmt.Fprintf(stdout, "Issue: %d\n", workItem.ProviderIssueNumber)
	fmt.Fprintf(stdout, "Title: %s\n", workItem.Title)
	fmt.Fprintf(stdout, "Status: %s\n", workItem.Status)
	fmt.Fprintf(stdout, "Provider updated: %s\n", workItem.ProviderUpdatedAt)
	fmt.Fprintf(stdout, "Refreshed: %s\n", workItem.RefreshedAt)
	fmt.Fprintf(stdout, "Event: %s\n", result.Event.Type)
	fmt.Fprintf(stdout, "Event ID: %d\n", result.Event.ID)
}

func printWorkItem(stdout io.Writer, workItem store.WorkItem) {
	fmt.Fprintf(stdout, "WorkItem %s\n", workItem.ProviderRef)
	fmt.Fprintf(stdout, "ID: %d\n", workItem.ID)
	fmt.Fprintf(stdout, "Repository: %s\n", workItem.RepositoryRef)
	fmt.Fprintf(stdout, "Issue: %d\n", workItem.ProviderIssueNumber)
	fmt.Fprintf(stdout, "Title: %s\n", workItem.Title)
	fmt.Fprintf(stdout, "Status: %s\n", workItem.Status)
	fmt.Fprintf(stdout, "Provider status: %s\n", workItem.ProviderStatusRaw)
	fmt.Fprintf(stdout, "URL: %s\n", workItem.URL)
	fmt.Fprintf(stdout, "Provider updated: %s\n", workItem.ProviderUpdatedAt)
	fmt.Fprintf(stdout, "Imported: %s\n", workItem.ImportedAt)
	fmt.Fprintf(stdout, "Refreshed: %s\n", workItem.RefreshedAt)
	fmt.Fprintf(stdout, "Body:\n%s\n", workItem.Body)
}
