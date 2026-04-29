package a2a

import (
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/require"
)

func TestArtifactStoreAddAndGet(t *testing.T) {
	t.Parallel()

	store := newArtifactStore()
	taskID := a2a.TaskID("task-1")
	art := &a2a.Artifact{
		ID:    a2a.NewArtifactID(),
		Name:  "test.txt",
		Parts: a2a.ContentParts{a2a.NewTextPart("content")},
	}

	store.Add(taskID, art)

	got := store.Get(taskID)
	require.Len(t, got, 1)
	require.Equal(t, "test.txt", got[0].Name)
}

func TestArtifactStoreGetEmpty(t *testing.T) {
	t.Parallel()

	store := newArtifactStore()
	got := store.Get("nonexistent")
	require.Empty(t, got)
}

func TestArtifactStoreTake(t *testing.T) {
	t.Parallel()

	store := newArtifactStore()
	taskID := a2a.TaskID("task-2")

	store.Add(taskID, &a2a.Artifact{ID: "a1", Name: "file1.txt", Parts: a2a.ContentParts{a2a.NewTextPart("c1")}})
	store.Add(taskID, &a2a.Artifact{ID: "a2", Name: "file2.txt", Parts: a2a.ContentParts{a2a.NewTextPart("c2")}})

	taken := store.Take(taskID)
	require.Len(t, taken, 2)
	require.Empty(t, store.Get(taskID))
}

func TestArtifactStoreCleanup(t *testing.T) {
	t.Parallel()

	store := newArtifactStore()

	taskID := a2a.TaskID("old-task")
	store.Add(taskID, &a2a.Artifact{ID: "a1", Name: "old.txt", Parts: a2a.ContentParts{a2a.NewTextPart("old")}})

	store.mu.Lock()
	store.timestamps[taskID] = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()

	recentID := a2a.TaskID("recent-task")
	store.Add(recentID, &a2a.Artifact{ID: "a2", Name: "new.txt", Parts: a2a.ContentParts{a2a.NewTextPart("new")}})

	store.Cleanup(1 * time.Hour)

	require.Empty(t, store.Get(taskID))
	require.Len(t, store.Get(recentID), 1)
}

func TestArtifactStoreGetReturnsCopy(t *testing.T) {
	t.Parallel()

	store := newArtifactStore()
	taskID := a2a.TaskID("task-copy")

	store.Add(taskID, &a2a.Artifact{ID: "a1", Name: "f.txt", Parts: a2a.ContentParts{a2a.NewTextPart("c")}})

	got := store.Get(taskID)
	got = append(got, &a2a.Artifact{ID: "extra"})

	require.Len(t, store.Get(taskID), 1)
}
