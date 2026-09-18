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

func TestHTTPHost(t *testing.T) {
	for _, test := range []struct {
		name string
		host string
		want string
	}{
		{name: "configured", host: "::1", want: "::1"},
		{name: "default", want: defaultHTTPHost},
		{name: "wildcard", host: "0.0.0.0", want: defaultHTTPHost},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(httpHostEnvVar, test.host)
			if got := httpHost(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestHTTPPort(t *testing.T) {
	t.Setenv(httpPortEnvVar, "")
	if got := httpPort(); got != defaultHTTPPort {
		t.Fatalf("got %q, want %q", got, defaultHTTPPort)
	}

	t.Setenv(httpPortEnvVar, "8401")
	if got := httpPort(); got != "8401" {
		t.Fatalf("got %q, want %q", got, "8401")
	}
}

func TestRootIncludesCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := &common.Context{Stdout: &stdout, Stderr: &stderr}
	rootCmd := root(ctx)

	for _, name := range []string{"providers", "install", "remove", "status"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if cmd == rootCmd || cmd.Name() != name {
			t.Fatalf("%s subcommand is not registered", name)
		}
	}
}
