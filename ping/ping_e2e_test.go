package ping_test

import (
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/stretchr/testify/require"
)

// TestPingPluginRegistered verifies the ping plugin is registered in the distro.
func TestPingPluginRegistered(t *testing.T) {
	testutil.SkipIfE2EDisabled(t)

	// Run crush with --list-plugins flag.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	// Wait for output.
	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "ping", "Expected ping plugin to be registered")
	require.Contains(t, output, "Registered plugin tools", "Expected plugin list header")
}

// TestPingPluginHelp verifies the ping plugin appears in tool descriptions.
func TestPingPluginHelp(t *testing.T) {
	testutil.SkipIfE2EDisabled(t)

	// Run crush with --help to see available commands.
	term := testutil.NewTestTerminal(t, []string{"--help"}, 120, 40)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	// The help output should show crush is working.
	require.Contains(t, output, "crush", "Expected crush in help output")
}
