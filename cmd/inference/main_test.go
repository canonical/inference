package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
)

func TestShareProvidersPath(t *testing.T) {
	t.Run("override", func(t *testing.T) {
		t.Setenv(sharedProvidersPathEnvVar, "/override/providers")
		t.Setenv("SNAP", "/snap/inference/current")
		if got := shareProvidersPath(); got != "/override/providers" {
			t.Fatalf("got %q, want override path", got)
		}
	})

	t.Run("snap default", func(t *testing.T) {
		t.Setenv(sharedProvidersPathEnvVar, "")
		t.Setenv("SNAP", "/snap/inference/current")
		want := filepath.Join("/snap/inference/current", "share/providers")
		if got := shareProvidersPath(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		t.Setenv(sharedProvidersPathEnvVar, "")
		t.Setenv("SNAP", "")
		if got := shareProvidersPath(); got != "" {
			t.Fatalf("got %q, want empty path", got)
		}
	})
}

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
