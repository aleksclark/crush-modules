// crush-a2a is an unofficial Crush build with the A2A protocol plugin.
//
// This build includes: a2a, kuri, otlp, agent-status, periodic-prompts, subagents, tempotown, tavily
// It uses the A2A v1.0 protocol instead of ACP for agent-to-agent communication.
//
// WARNING: This is NOT an official Charm Labs release.
package main

import (
	"github.com/charmbracelet/crush/cmd/crush"

	// Import plugins - they register themselves via init()
	_ "github.com/aleksclark/crush-modules/a2a"
	_ "github.com/aleksclark/crush-modules/agent-status"
	_ "github.com/aleksclark/crush-modules/kuri"
	_ "github.com/aleksclark/crush-modules/otlp"
	_ "github.com/aleksclark/crush-modules/periodic-prompts"
	_ "github.com/aleksclark/crush-modules/subagents"
	_ "github.com/aleksclark/crush-modules/tavily"
	_ "github.com/aleksclark/crush-modules/tempotown"
)

func main() {
	crush.Execute()
}
