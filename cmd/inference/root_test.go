package main

import "testing"

func TestRootIncludesProvidersCommand(t *testing.T) {
	ctx, _, _ := newTestContext(&fakeRegistry{}, &fakeStatusSource{})
	rootCmd := root(ctx)
	cmd, _, err := rootCmd.Find([]string{"providers"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == rootCmd || cmd.Name() != "providers" {
		t.Fatal("providers subcommand is not registered")
	}
}

func TestRootIncludesHiddenSnapCatalogSeedCommand(t *testing.T) {
	ctx, _, _ := newTestContext(&fakeRegistry{}, &fakeStatusSource{})
	rootCmd := root(ctx)
	cmd, _, err := rootCmd.Find([]string{"generate-snap-catalog-seed"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == rootCmd || !cmd.Hidden {
		t.Fatal("hidden snap catalog seed subcommand is not registered")
	}
}
