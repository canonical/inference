package main

import "testing"

func TestRootIncludesProvidersCommand(t *testing.T) {
	ctx, _, _ := newTestContext()
	rootCmd := root(ctx)
	cmd, _, err := rootCmd.Find([]string{"providers"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == rootCmd || cmd.Name() != "providers" {
		t.Fatal("providers subcommand is not registered")
	}
}
