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

	for _, name := range []string{"find", "providers", "install", "remove", "status"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if cmd == rootCmd || cmd.Name() != name {
			t.Fatalf("%s subcommand is not registered", name)
		}
	}
}

func TestCommandGroups(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := &common.Context{Stdout: &stdout, Stderr: &stderr}
	rootCmd := root(ctx)

	commandGroups := map[string]string{
		"status":    basicsGroupID,
		"models":    basicsGroupID,
		"hardware":  basicsGroupID,
		"find":      providersGroupID,
		"providers": providersGroupID,
		"info":      providersGroupID,
		"install":   providersGroupID,
		"remove":    providersGroupID,
	}

	for name, wantGroup := range commandGroups {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			if cmd.GroupID != wantGroup {
				t.Errorf("got GroupID %q, want %q", cmd.GroupID, wantGroup)
			}
			if !rootCmd.ContainsGroup(wantGroup) {
				t.Errorf("group %q is not registered", wantGroup)
			}
		})
	}
}

func TestRootIncludesModelsCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := &common.Context{Stdout: &stdout, Stderr: &stderr}
	rootCmd := root(ctx)
	cmd, _, err := rootCmd.Find([]string{"models"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == rootCmd || cmd.Name() != "models" {
		t.Fatal("models subcommand is not registered")
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "unknown command", args: []string{"bogus"}, wantErr: true},
		{name: "unknown command with help flag", args: []string{"blah", "-h"}, wantErr: true},
		{name: "no args", args: nil, wantErr: false},
		{name: "help flag", args: []string{"-h"}, wantErr: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			ctx := &common.Context{Stdout: &stdout, Stderr: &stderr}
			rootCmd := root(ctx)
			rootCmd.SetArgs(test.args)

			err := rootCmd.Execute()
			if test.wantErr && err == nil {
				t.Fatalf("got nil error, want error for args %v", test.args)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("got error %v, want nil for args %v", err, test.args)
			}
		})
	}
}
