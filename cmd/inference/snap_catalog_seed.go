package main

import (
	"fmt"
	"os"
	"time"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/spf13/cobra"
)

func SnapCatalogSeed() *cobra.Command {
	return snapCatalogSeed(snapcatalog.NewHTTPFetcher(), time.Now)
}

func snapCatalogSeed(fetcher snapcatalog.Fetcher, now func() time.Time) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "generate-snap-catalog-seed OUTPUT",
		Short:             "Generate the packaged snap catalog seed",
		Hidden:            true,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := snapcatalog.FetchEntries(cmd.Context(), fetcher)
			if err != nil {
				return fmt.Errorf("fetching snap catalog seed: %w", err)
			}
			data, err := snapcatalog.EncodeDocument(snapcatalog.Document{
				SchemaVersion: snapcatalog.SchemaVersion,
				FetchedAt:     now().UTC(),
				Entries:       entries,
			})
			if err != nil {
				return fmt.Errorf("encoding snap catalog seed: %w", err)
			}
			if err := os.WriteFile(args[0], data, 0o644); err != nil {
				return fmt.Errorf("writing snap catalog seed: %w", err)
			}
			return nil
		},
	}
	return cmd
}
