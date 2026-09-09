package main

import (
	"bytes"
	"testing"

	"github.com/canonical/inference/cmd/cli/common"
)

func TestRootIncludesProvidersCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := &common.Context{Stdout: &stdout, Stderr: &stderr}
	rootCmd := root(ctx)
	cmd, _, err := rootCmd.Find([]string{"providers"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == rootCmd || cmd.Name() != "providers" {
		t.Fatal("providers subcommand is not registered")
	}
}
