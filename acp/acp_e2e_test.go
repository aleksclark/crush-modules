//go:build e2e

package acp_test

import (
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/stretchr/testify/require"
)

// TestACPPluginRegistered verifies the ACP tools are registered in the distro.
func TestACPPluginRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "acp_list_agents", "Expected acp_list_agents tool to be registered")
	require.Contains(t, output, "acp_run_agent", "Expected acp_run_agent tool to be registered")
	require.Contains(t, output, "acp_resume_run", "Expected acp_resume_run tool to be registered")
	require.Contains(t, output, "acp-server", "Expected acp-server hook to be registered")
}
