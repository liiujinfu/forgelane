package cli

import (
	"fmt"

	githubprovider "github.com/liiujinfu/forgelane/internal/provider/github"
	gitlabprovider "github.com/liiujinfu/forgelane/internal/provider/gitlab"
	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/liiujinfu/forgelane/internal/workitems"
)

func workItemProviderForRef(options Options, ref workitems.ProviderRef) (workitems.Provider, error) {
	if options.WorkItemProvider != nil {
		return options.WorkItemProvider, nil
	}
	if options.WorkItemProviderFactory != nil {
		return options.WorkItemProviderFactory(ref)
	}
	switch ref.Provider {
	case "github":
		return githubprovider.NewIssueProvider(githubprovider.Options{}), nil
	case "gitlab":
		return gitlabprovider.NewIssueProvider(gitlabprovider.Options{}), nil
	default:
		return nil, fmt.Errorf("unsupported WorkItem provider %q", ref.Provider)
	}
}

func changeProviderForProvider(options Options, provider string) (workflow.ChangeProvider, error) {
	if options.ChangeProvider != nil {
		return options.ChangeProvider, nil
	}
	if options.ChangeProviderFactory != nil {
		return options.ChangeProviderFactory(provider)
	}
	if options.WorkItemProvider != nil {
		return nil, nil
	}
	switch provider {
	case "github":
		return githubprovider.NewChangeProvider(githubprovider.ChangeProviderOptions{}), nil
	case "gitlab":
		return gitlabprovider.NewChangeProvider(gitlabprovider.ChangeProviderOptions{}), nil
	default:
		return nil, fmt.Errorf("unsupported ChangeProvider %q", provider)
	}
}

func changeProviderForRun(options Options, instanceStore *store.Store, runID int64) (workflow.ChangeProvider, error) {
	if options.ChangeProvider != nil {
		return options.ChangeProvider, nil
	}
	if options.ChangeProviderFactory == nil && options.WorkItemProvider != nil {
		return nil, nil
	}
	detail, err := instanceStore.GetAgentRunDetail(runID)
	if err != nil {
		return nil, err
	}
	return changeProviderForProvider(options, detail.WorkItem.Provider)
}
