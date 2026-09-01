package catalog

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// defaultResolveConcurrency bounds how many repository lookups run at once.
const defaultResolveConcurrency = 4

// NameResolver resolves a repository's authoritative snap name from its
// snap/snapcraft.yaml. Implemented by GitHubClient.
type NameResolver interface {
	SnapcraftName(ctx context.Context, fullName string) (string, error)
}

// ResolveCandidate fetches the public catalog and resolves every public
// repository's authoritative snap name into a validated, sorted set of
// providers.
//
// Resolution is all-or-nothing: if any repository fails to resolve, or the
// resulting names contain a duplicate, the whole candidate is discarded and
// an error is returned. Concurrency <= 0 uses a default bound.
func ResolveCandidate(ctx context.Context, fetcher CatalogFetcher, resolver NameResolver, concurrency int) ([]Provider, error) {
	data, err := fetcher.FetchCatalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching public catalog: %w", err)
	}

	remote, err := ParseRemoteCatalog(data)
	if err != nil {
		return nil, err
	}

	repos := PublicRepositories(remote)
	if len(repos) == 0 {
		return nil, fmt.Errorf("public catalog has no public repositories")
	}

	if concurrency <= 0 {
		concurrency = defaultResolveConcurrency
	}

	type resolved struct {
		name string
		err  error
	}
	results := make([]resolved, len(repos))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func(i int, repo RemoteRepository) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			name, err := resolver.SnapcraftName(ctx, repo.FullName)
			results[i] = resolved{name: name, err: err}
		}(i, repo)
	}
	wg.Wait()

	providers := make([]Provider, 0, len(repos))
	seenNames := make(map[string]string, len(repos))   // name -> full_name
	seenRepos := make(map[string]struct{}, len(repos)) // full_name seen
	for i, repo := range repos {
		if _, dup := seenRepos[repo.FullName]; dup {
			return nil, fmt.Errorf("duplicate repository %q in public catalog", repo.FullName)
		}
		seenRepos[repo.FullName] = struct{}{}

		r := results[i]
		if r.err != nil {
			return nil, fmt.Errorf("resolving %s: %w", repo.FullName, r.err)
		}
		if existing, dup := seenNames[r.name]; dup {
			return nil, fmt.Errorf("duplicate snap name %q resolved from %s and %s", r.name, existing, repo.FullName)
		}
		seenNames[r.name] = repo.FullName

		providers = append(providers, Provider{Name: r.name, FullName: repo.FullName})
	}

	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })

	return providers, nil
}
