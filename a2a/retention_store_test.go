package a2a

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/stretchr/testify/require"
)

func TestRetentionStoreCreateAndGet(t *testing.T) {
	t.Parallel()
	store := newRetentionStore(time.Hour, slog.Default())

	ctx := context.Background()
	task := &a2a.Task{
		ID:        "task-1",
		ContextID: "ctx-1",
		Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
	}

	version, err := store.Create(ctx, task)
	require.NoError(t, err)
	require.NotEqual(t, taskstore.TaskVersionMissing, version)

	got, err := store.Get(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, a2a.TaskID("task-1"), got.Task.ID)
	require.Equal(t, a2a.TaskStateWorking, got.Task.Status.State)
}

func TestRetentionStoreUpdateToCompleted(t *testing.T) {
	t.Parallel()
	store := newRetentionStore(time.Hour, slog.Default())

	ctx := context.Background()
	task := &a2a.Task{
		ID:        "task-2",
		ContextID: "ctx-2",
		Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
	}

	version, err := store.Create(ctx, task)
	require.NoError(t, err)

	// Update to completed state.
	completedTask := &a2a.Task{
		ID:        "task-2",
		ContextID: "ctx-2",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	_, err = store.Update(ctx, &taskstore.UpdateRequest{
		Task:        completedTask,
		PrevVersion: version,
	})
	require.NoError(t, err)

	// GetTask should still find the completed task.
	got, err := store.Get(ctx, "task-2")
	require.NoError(t, err)
	require.Equal(t, a2a.TaskStateCompleted, got.Task.Status.State)
	require.Equal(t, 1, store.TaskCount())
}

func TestRetentionStoreGetNotFound(t *testing.T) {
	t.Parallel()
	store := newRetentionStore(time.Hour, slog.Default())

	_, err := store.Get(context.Background(), "nonexistent")
	require.ErrorIs(t, err, a2a.ErrTaskNotFound)
}

func TestRetentionStoreMultipleTasks(t *testing.T) {
	t.Parallel()
	store := newRetentionStore(time.Hour, slog.Default())
	ctx := context.Background()

	// Create several tasks and complete them.
	for i, id := range []a2a.TaskID{"t1", "t2", "t3"} {
		task := &a2a.Task{
			ID:        id,
			ContextID: "ctx",
			Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
		}
		v, err := store.Create(ctx, task)
		require.NoError(t, err)

		completed := &a2a.Task{
			ID:        id,
			ContextID: "ctx",
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}
		_, err = store.Update(ctx, &taskstore.UpdateRequest{
			Task:        completed,
			PrevVersion: v,
		})
		require.NoError(t, err, "task %d", i)
	}

	// All tasks should be retrievable.
	for _, id := range []a2a.TaskID{"t1", "t2", "t3"} {
		got, err := store.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, a2a.TaskStateCompleted, got.Task.Status.State)
	}

	require.Equal(t, 3, store.TaskCount())
}
