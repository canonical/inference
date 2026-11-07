package snap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newMockSnapctl(t *testing.T) (string, string) {
	t.Helper()

	binDir := t.TempDir()
	snapctlLog := filepath.Join(t.TempDir(), "snapctl.log")
	snapctlPath := filepath.Join(binDir, "snapctl")
	snapctl := `#!/bin/sh
printf '%s\n' "$*" >> "$SNAPCTL_LOG"
case "$1" in
    get)
        case "$2" in
            http.host) printf '%s\n' "$TEST_HOST" ;;
            http.port) printf '%s\n' "$TEST_PORT" ;;
            http.cors-allow-all) printf '%s\n' "$TEST_CORS_ALLOW_ALL" ;;
            *) exit 1 ;;
        esac
        ;;
    set|restart) ;;
    *) exit 1 ;;
esac
`
	if err := os.WriteFile(snapctlPath, []byte(snapctl), 0o755); err != nil {
		t.Fatal(err)
	}

	return binDir, snapctlLog
}

func TestConfigureHook(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		port         string
		corsAllowAll string
		wantErr      string
		wantSnapctl  string
	}{
		{name: "valid", host: "::1", port: "8400", wantSnapctl: "get http.host\nget http.port\nget http.cors-allow-all\nrestart inference_gpu\n"},
		{name: "CORS enabled", host: "::1", port: "8400", corsAllowAll: "true", wantSnapctl: "get http.host\nget http.port\nget http.cors-allow-all\nrestart inference_gpu\n"},
		{name: "CORS disabled", host: "::1", port: "8400", corsAllowAll: "false", wantSnapctl: "get http.host\nget http.port\nget http.cors-allow-all\nrestart inference_gpu\n"},
		{name: "invalid CORS option", host: "::1", port: "8400", corsAllowAll: "invalid", wantErr: "http.cors-allow-all must be true or false", wantSnapctl: "get http.host\nget http.port\nget http.cors-allow-all\n"},
		{name: "numeric CORS option", host: "::1", port: "8400", corsAllowAll: "1", wantErr: "http.cors-allow-all must be true or false", wantSnapctl: "get http.host\nget http.port\nget http.cors-allow-all\n"},
		{name: "empty host", port: "8400", wantErr: "http.host must not be empty", wantSnapctl: "get http.host\nget http.port\n"},
		{name: "non-numeric port", host: "127.0.0.1", port: "invalid", wantErr: "http.port must be an integer between 1 and 65535", wantSnapctl: "get http.host\nget http.port\n"},
		{name: "zero-prefixed port", host: "127.0.0.1", port: "08400", wantErr: "http.port must be an integer between 1 and 65535", wantSnapctl: "get http.host\nget http.port\n"},
		{name: "zero port", host: "127.0.0.1", port: "0", wantErr: "http.port must be an integer between 1 and 65535", wantSnapctl: "get http.host\nget http.port\n"},
		{name: "port too high", host: "127.0.0.1", port: "65536", wantErr: "http.port must be an integer between 1 and 65535", wantSnapctl: "get http.host\nget http.port\n"},
		{name: "oversized port", host: "127.0.0.1", port: "99999999999999999999", wantErr: "http.port must be an integer between 1 and 65535", wantSnapctl: "get http.host\nget http.port\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binDir, snapctlLog := newMockSnapctl(t)

			command := exec.Command("sh", "hooks/configure")
			command.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"SNAP_INSTANCE_NAME=inference_gpu",
				"TEST_HOST="+test.host,
				"TEST_PORT="+test.port,
				"TEST_CORS_ALLOW_ALL="+test.corsAllowAll,
				"SNAPCTL_LOG="+snapctlLog,
			)
			output, err := command.CombinedOutput()
			snapctlCommands, readErr := os.ReadFile(snapctlLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(snapctlCommands) != test.wantSnapctl {
				t.Fatalf("snapctl commands %q, want %q", snapctlCommands, test.wantSnapctl)
			}
			if test.wantErr != "" {
				if err == nil {
					t.Fatal("expected configure hook to fail")
				}
				if !strings.Contains(string(output), test.wantErr) {
					t.Fatalf("got output %q, want %q", output, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("configure hook failed: %v: %s", err, output)
			}
		})
	}
}

func TestPostRefreshHookInitializesMissingConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		port         string
		corsAllowAll string
		wantSnapctl  string
	}{
		{
			name:        "missing configuration",
			wantSnapctl: "get http.port\nset http.port=8400\nget http.host\nset http.host=127.0.0.1\nget http.cors-allow-all\nset http.cors-allow-all=false\n",
		},
		{
			name:         "existing configuration",
			host:         "0.0.0.0",
			port:         "9000",
			corsAllowAll: "true",
			wantSnapctl:  "get http.port\nget http.host\nget http.cors-allow-all\n",
		},
		{
			name:        "missing host",
			port:        "9000",
			wantSnapctl: "get http.port\nget http.host\nset http.host=127.0.0.1\nget http.cors-allow-all\nset http.cors-allow-all=false\n",
		},
		{
			name:         "missing port",
			host:         "::1",
			corsAllowAll: "false",
			wantSnapctl:  "get http.port\nset http.port=8400\nget http.host\nget http.cors-allow-all\n",
		},
		{
			name:        "missing CORS option",
			host:        "0.0.0.0",
			port:        "9000",
			wantSnapctl: "get http.port\nget http.host\nget http.cors-allow-all\nset http.cors-allow-all=false\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binDir, snapctlLog := newMockSnapctl(t)
			snapDir := t.TempDir()
			commonDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(snapDir, "onboarded-snaps.json"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}

			command := exec.Command("sh", "hooks/post-refresh")
			command.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"SNAP="+snapDir,
				"SNAP_COMMON="+commonDir,
				"TEST_HOST="+test.host,
				"TEST_PORT="+test.port,
				"TEST_CORS_ALLOW_ALL="+test.corsAllowAll,
				"SNAPCTL_LOG="+snapctlLog,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("post-refresh hook failed: %v: %s", err, output)
			}

			snapctlCommands, err := os.ReadFile(snapctlLog)
			if err != nil {
				t.Fatal(err)
			}
			if string(snapctlCommands) != test.wantSnapctl {
				t.Fatalf("snapctl commands %q, want %q", snapctlCommands, test.wantSnapctl)
			}
			if _, err := os.Stat(filepath.Join(commonDir, "onboarded-snaps.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallHookInitializesConfiguration(t *testing.T) {
	binDir, snapctlLog := newMockSnapctl(t)
	snapDir := t.TempDir()
	commonDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(snapDir, "onboarded-snaps.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "hooks/install")
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SNAP="+snapDir,
		"SNAP_COMMON="+commonDir,
		"SNAPCTL_LOG="+snapctlLog,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install hook failed: %v: %s", err, output)
	}
	commands, err := os.ReadFile(snapctlLog)
	if err != nil {
		t.Fatal(err)
	}
	if want := "set http.port=8400\nset http.host=127.0.0.1\nset http.cors-allow-all=false\n"; string(commands) != want {
		t.Fatalf("snapctl commands %q, want %q", commands, want)
	}
	if _, err := os.Stat(filepath.Join(commonDir, "onboarded-snaps.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSeedEnvironment(t *testing.T) {
	for _, corsAllowAll := range []string{"", "false", "true"} {
		t.Run(corsAllowAll, func(t *testing.T) {
			binDir, snapctlLog := newMockSnapctl(t)
			command := exec.Command("bash", "local/seed-env.sh", "sh", "-c",
				`printf '%s\n' "$HTTP_HOST" "$HTTP_PORT" "$HTTP_CORS_ALLOW_ALL"`)
			command.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TEST_HOST=::1",
				"TEST_PORT=9000",
				"TEST_CORS_ALLOW_ALL="+corsAllowAll,
				"SNAPCTL_LOG="+snapctlLog,
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("seed environment failed: %v: %s", err, output)
			}
			if want := "::1\n9000\n" + corsAllowAll + "\n"; string(output) != want {
				t.Fatalf("environment output %q, want %q", output, want)
			}
		})
	}
}
