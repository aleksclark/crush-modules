// crush-extended is an unofficial Crush build with community plugins.
//
// This build includes: otlp, agent-status, periodic-prompts
//
// WARNING: This is NOT an official Charm Labs release.
package main

import (
	"github.com/charmbracelet/crush/cmd/crush"

	// Import plugins - they register themselves via init()
	_ "github.com/aleksclark/crush-modules/agent-status"
	_ "github.com/aleksclark/crush-modules/otlp"
	_ "github.com/aleksclark/crush-modules/periodic-prompts"
)

func main() {
	crush.Execute()
}
