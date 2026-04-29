//go:build e2e

package a2a_test

import (
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/stretchr/testify/require"
)

func TestA2APluginToolsRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 120, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "a2a_list_agents", "Expected a2a_list_agents tool to be registered")
	require.Contains(t, output, "a2a_send_message", "Expected a2a_send_message tool to be registered")
	require.Contains(t, output, "a2a_get_task", "Expected a2a_get_task tool to be registered")
	require.Contains(t, output, "a2a_attach_file", "Expected a2a_attach_file tool to be registered")
	require.Contains(t, output, "a2a-server", "Expected a2a-server hook to be registered")
}
