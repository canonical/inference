// Package command wires Cobra commands to their dependencies and owns all
// output rendering. Commands validate input, call into internal/providers for
// behavior, and print results through the injected writers.
package command

import (
	"io"

	"github.com/canonical/inference/internal/providers"
)

// Context carries the services and I/O streams a command needs. It is
// constructed once in main and passed to every command factory, so tests can
// inject fakes without touching global state.
type Context struct {
	// Stdout is where command output (table/JSON) is written.
	Stdout io.Writer
	// Stderr is where warnings and diagnostics are written.
	Stderr io.Writer

	// Providers lists providers by joining the catalog with snapd state.
	Providers *providers.Service
}
