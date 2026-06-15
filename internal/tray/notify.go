package tray

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/godbus/dbus/v5"
)

// notify sends a desktop notification via org.freedesktop.Notifications. Best
// effort: any failure (no session bus, no notification daemon) is ignored — the
// menu still works without it.
func notify(summary, body string) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	obj.Call("org.freedesktop.Notifications.Notify", 0,
		"Inference",      // app_name
		uint32(0),        // replaces_id
		"",               // app_icon
		summary,          // summary
		body,             // body
		[]string{},       // actions
		map[string]dbus.Variant{}, // hints
		int32(5000),      // timeout ms
	)
}

// sessionBusAvailable reports whether a session bus we can register on exists.
// It checks for a resolvable address WITHOUT letting godbus autolaunch a new bus
// (which would hang headless); only then does it actually connect.
func sessionBusAvailable() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		// godbus would fall back to $XDG_RUNTIME_DIR/bus, then to autolaunch
		// (which hangs headless). Only proceed if we can see a real bus socket.
		// Under a snap, XDG_RUNTIME_DIR is remapped to a per-snap dir with no
		// bus, so also probe the canonical /run/user/<uid>/bus.
		candidates := []string{
			filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "bus"),
			fmt.Sprintf("/run/user/%d/bus", os.Getuid()),
		}
		found := false
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	return conn != nil
}
