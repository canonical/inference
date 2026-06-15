package tray

import (
	"os"
	"path/filepath"
	"strings"
)

const desktopFile = "inference-tray.desktop"

const autostartBody = `[Desktop Entry]
Type=Application
Name=Inference Indicator
Comment=Top-bar status and controls for local AI
Exec=inference tray
Icon=inference
Terminal=false
X-GNOME-Autostart-enabled=true
`

// autostartPath is the per-user autostart entry. Under a snap this resolves to
// $SNAP_USER_DATA/.config/autostart via XDG_CONFIG_HOME.
func autostartPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", desktopFile), nil
}

// AutostartEnabled reports whether the tray is set to launch on login.
func AutostartEnabled() bool {
	p, err := autostartPath()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return !strings.Contains(string(b), "Hidden=true")
}

// SetAutostart enables or disables launch-on-login by writing the user autostart
// entry (disabling writes a Hidden override so GNOME skips it).
func SetAutostart(on bool) error {
	p, err := autostartPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body := autostartBody
	if !on {
		body += "Hidden=true\n"
	}
	return os.WriteFile(p, []byte(body), 0o644)
}
