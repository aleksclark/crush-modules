package a2a

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

const (
	// TaskRetentionTTL is how long completed tasks are retained before cleanup.
	TaskRetentionTTL = 1 * time.Hour

	// TaskCleanupInterval is how often expired tasks are cleaned up.
	TaskCleanupInterval = 5 * time.Minute
)

// retentionStore wraps an in-memory taskstore.Store with lifecycle logging and
// TTL-based cleanup of terminal tasks. This ensures that GetTask works reliably
// for completed tasks while preventing unbounded memory growth in long-running
// server processes.
type retentionStore struct {
	inner  *taskstore.InMemory
	logger *slog.Logger

	mu          sync.Mutex
	completedAt map[a2a.TaskID]time.Time
	retention   time.Duration
}

// newRetentionStore creates a retentionStore with a fresh in-memory store.
// The authenticator is a no-op since crush does not use A2A authentication.
func newRetentionStore(retention time.Duration, logger *slog.Logger) *retentionStore {
	if retention <= 0 {
		retention = TaskRetentionTTL
	}
	inner := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: func(_ context.Context) (string, error) {
			// Crush runs as a single-user local server; no auth needed.
			return "crush", nil
		},
	})
	return &retentionStore{
		inner:       inner,
		logger:      logger,
		completedAt: make(map[a2a.TaskID]time.Time),
		retention:   retention,
	}
}

// Create implements taskstore.Store.
func (s *retentionStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	version, err := s.inner.Create(ctx, task)
	if err != nil {
		s.logger.Warn("task store create failed", "task_id", task.ID, "error", err)
		return version, err
	}
	s.logger.Debug("task created", "task_id", task.ID, "context_id", task.ContextID, "state", task.Status.State)
	if task.Status.State.Terminal() {
		s.trackTerminal(task.ID)
	}
	return version, nil
}

// Update implements taskstore.Store.
func (s *retentionStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	version, err := s.inner.Update(ctx, req)
	if err != nil {
		s.logger.Warn("task store update failed", "task_id", req.Task.ID, "error", err)
		return version, err
	}
	if req.Task.Status.State.Terminal() {
		s.trackTerminal(req.Task.ID)
		s.logger.Debug("task completed", "task_id", req.Task.ID, "state", req.Task.Status.State)
	}
	return version, nil
}

// Get implements taskstore.Store.
func (s *retentionStore) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	result, err := s.inner.Get(ctx, taskID)
	if err != nil {
		s.logger.Debug("task store get miss", "task_id", taskID, "error", err)
		return nil, err
	}
	return result, nil
}

// List implements taskstore.Store.
func (s *retentionStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return s.inner.List(ctx, req)
}

func (s *retentionStore) trackTerminal(taskID a2a.TaskID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completedAt[taskID] = time.Now()
}

// TaskCount returns the number of tasks currently tracked for completion.
func (s *retentionStore) TaskCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.completedAt)
}
