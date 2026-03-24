// Package sdk provides a high-level Go client for the Crush ACP (Agent
// Communication Protocol).
//
// The SDK wraps the ACP REST API with a session-oriented interface for
// managing multi-turn conversations with Crush agents. It supports:
//
//   - Starting new sessions and sending prompts
//   - Resuming existing sessions across agent restarts
//   - Dumping session history for persistence
//   - Restoring sessions from saved snapshots
//   - Streaming responses with real-time text deltas
//   - Crash recovery via session export/import
//
// # Quick Start
//
//	client := sdk.NewClient("http://localhost:8199")
//
//	// Start a new session.
//	session, err := client.NewSession(ctx, "Fix the login bug")
//	// session.ID is now set, text is in session.Text()
//
//	// Continue the conversation.
//	session, err = client.Resume(ctx, session.ID, "Now add tests for it")
//
//	// Save the session for later.
//	snapshot, err := client.Dump(ctx, session.ID)
//	data, _ := json.Marshal(snapshot)
//	os.WriteFile("session.json", data, 0o644)
//
//	// Restore on a new agent instance.
//	err = client.Restore(ctx, snapshot)
//	session, err = client.Resume(ctx, snapshot.Session.ID, "What did we do last time?")
//
// # Streaming
//
// For real-time output, use the streaming variants:
//
//	stream, err := client.NewSessionStream(ctx, "Explain auth.go")
//	for event := range stream.Events {
//	    switch event.Type {
//	    case sdk.EventMessagePart:
//	        fmt.Print(event.Part.Content)
//	    case sdk.EventRunCompleted:
//	        fmt.Println("\nDone:", event.Run.SessionID)
//	    }
//	}
//	if err := stream.Err(); err != nil {
//	    log.Fatal(err)
//	}
package sdk
