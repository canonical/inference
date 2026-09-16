package snap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureHook(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		port    string
		wantErr string
	}{
		{name: "valid", host: "::1", port: "08400"},
		{name: "empty host", port: "8400", wantErr: "http.host must not be empty"},
		{name: "non-numeric port", host: "127.0.0.1", port: "invalid", wantErr: "http.port must be an integer between 1 and 65535"},
		{name: "zero port", host: "127.0.0.1", port: "0", wantErr: "http.port must be an integer between 1 and 65535"},
		{name: "port too high", host: "127.0.0.1", port: "65536", wantErr: "http.port must be an integer between 1 and 65535"},
		{name: "oversized port", host: "127.0.0.1", port: "99999999999999999999", wantErr: "http.port must be an integer between 1 and 65535"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binDir := t.TempDir()
			restartLog := filepath.Join(t.TempDir(), "restart.log")
			snapctlPath := filepath.Join(binDir, "snapctl")
			snapctl := `#!/bin/sh
case "$1" in
    get)
        case "$2" in
            http.host) printf '%s\n' "$TEST_HOST" ;;
            http.port) printf '%s\n' "$TEST_PORT" ;;
            *) exit 1 ;;
        esac
        ;;
    restart) printf '%s\n' "$2" > "$RESTART_LOG" ;;
    *) exit 1 ;;
esac
`
			if err := os.WriteFile(snapctlPath, []byte(snapctl), 0o755); err != nil {
				t.Fatal(err)
			}

			command := exec.Command("sh", "hooks/configure")
			command.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"SNAP_INSTANCE_NAME=inference_gpu",
				"TEST_HOST="+test.host,
				"TEST_PORT="+test.port,
				"RESTART_LOG="+restartLog,
			)
			output, err := command.CombinedOutput()
			if test.wantErr != "" {
				if err == nil {
					t.Fatal("expected configure hook to fail")
				}
				if !strings.Contains(string(output), test.wantErr) {
					t.Fatalf("got output %q, want %q", output, test.wantErr)
				}
				if _, statErr := os.Stat(restartLog); !os.IsNotExist(statErr) {
					t.Fatalf("service restarted after invalid configuration: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("configure hook failed: %v: %s", err, output)
			}
			restarted, err := os.ReadFile(restartLog)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(restarted)); got != "inference_gpu" {
				t.Fatalf("restarted %q, want %q", got, "inference_gpu")
			}
		})
	}
}
