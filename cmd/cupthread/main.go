// Command cupthread is the official CupThread CLI. It lets developers manage
// the projects they created on cupthread.com — everything the web Console can
// do — and links this repo's agent skills into editor/agent directories.
package main

import (
	"os"

	"github.com/CupThread/CupThreadAgenticCoding/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
