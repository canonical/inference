// Command inference is the entry point for the inference CLI.
package main

import (
	"os"

	"github.com/canonical/inference/internal/command"
)

func main() {
	ctx := &command.Context{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	root := command.Root(ctx)
	root.SetOut(ctx.Stdout)
	root.SetErr(ctx.Stderr)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
