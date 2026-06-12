// Command inference is the orchestration front-door for local AI on Ubuntu:
// hardware detection, driver advice, silicon-optimal model/engine install, and
// a federated OpenAI-compatible proxy over inference snaps and external models.
package main

import (
	"os"

	"inference/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
