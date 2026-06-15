package tray

import _ "embed"

// Indicator icons (22px discs), embedded so the binary stays self-contained.
var (
	//go:embed assets/active.png
	iconActive []byte
	//go:embed assets/attention.png
	iconAttention []byte
	//go:embed assets/idle.png
	iconIdle []byte
)

// iconFor returns the PNG bytes for a state.
func iconFor(s State) []byte {
	switch s {
	case StateActive:
		return iconActive
	case StateAttention:
		return iconAttention
	default:
		return iconIdle
	}
}
