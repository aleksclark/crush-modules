package acp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunStoreCreateAndGet(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{
		RunID:     "run-1",
		AgentName: "test",
		Status:    RunStatusCreated,
		Output:    []Message{},
		CreatedAt: time.Now(),
	}

	rd := store.create(run)
	require.NotNil(t, rd)

	got := store.get("run-1")
	require.NotNil(t, got)
	require.Equal(t, "run-1", got.getRun().RunID)
}

func TestRunStoreGetMissing(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	require.Nil(t, store.get("nonexistent"))
}

func TestRunStoreDelete(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-del", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	store.create(run)

	store.delete("run-del")
	require.Nil(t, store.get("run-del"))
}

func TestRunDataStatusTransitions(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-status", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := store.create(run)

	rd.setStatus(RunStatusInProgress)
	require.Equal(t, RunStatusInProgress, rd.getRun().Status)
	require.Nil(t, rd.getRun().FinishedAt)

	rd.setStatus(RunStatusCompleted)
	require.Equal(t, RunStatusCompleted, rd.getRun().Status)
	require.NotNil(t, rd.getRun().FinishedAt)
}

func TestRunDataEmitAndGetEvents(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-events", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := store.create(run)

	rd.emit(Event{Type: EventRunCreated})
	rd.emit(Event{Type: EventRunInProgress})
	rd.emit(Event{Type: EventMessagePart, Part: &MessagePart{ContentType: "text/plain", Content: "hello"}})

	events := rd.getEvents()
	require.Len(t, events, 3)
	require.Equal(t, EventRunCreated, events[0].Type)
	require.Equal(t, EventMessagePart, events[2].Type)
}

func TestRunDataSubscribe(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-sub", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := store.create(run)

	sub := rd.subscribe()

	rd.emit(Event{Type: EventRunInProgress})
	rd.emit(Event{Type: EventMessagePart, Part: &MessagePart{ContentType: "text/plain", Content: "hi"}})

	event1 := <-sub
	require.Equal(t, EventRunInProgress, event1.Type)

	event2 := <-sub
	require.Equal(t, EventMessagePart, event2.Type)
	require.Equal(t, "hi", event2.Part.Content)

	rd.setStatus(RunStatusCompleted)
	rd.emit(Event{Type: EventRunCompleted})

	// Channel should close after terminal state.
	for range sub {
	}
}

func TestRunDataSetOutput(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-out", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := store.create(run)

	output := []Message{NewAgentMessage("result")}
	rd.setOutput(output)

	got := rd.getRun()
	require.Len(t, got.Output, 1)
	require.Equal(t, "result", got.Output[0].Parts[0].Content)
}

func TestRunDataSetError(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-err", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := store.create(run)

	rd.setError(&ACPError{Code: 500, Message: "something went wrong"})
	got := rd.getRun()
	require.NotNil(t, got.Error)
	require.Equal(t, "something went wrong", got.Error.Message)
}

func TestRunStoreCleanup(t *testing.T) {
	t.Parallel()

	store := newRunStore()

	old := Run{RunID: "old", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now().Add(-2 * time.Hour)}
	rdOld := store.create(old)
	rdOld.setStatus(RunStatusCompleted)

	recent := Run{RunID: "recent", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rdRecent := store.create(recent)
	rdRecent.setStatus(RunStatusCompleted)

	active := Run{RunID: "active", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now().Add(-2 * time.Hour)}
	store.create(active)

	store.cleanup(1 * time.Hour)

	require.Nil(t, store.get("old"))
	require.NotNil(t, store.get("recent"))
	require.NotNil(t, store.get("active"))
}

func TestRunDataDoneChannel(t *testing.T) {
	t.Parallel()

	store := newRunStore()
	run := Run{RunID: "run-done", AgentName: "test", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := store.create(run)

	select {
	case <-rd.done:
		t.Fatal("done should not be closed yet")
	default:
	}

	rd.setStatus(RunStatusFailed)

	select {
	case <-rd.done:
	case <-time.After(time.Second):
		t.Fatal("done should be closed after terminal status")
	}
}
