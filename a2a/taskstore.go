package a2a

import (
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

type artifactStore struct {
	mu         sync.RWMutex
	artifacts  map[a2a.TaskID][]*a2a.Artifact
	timestamps map[a2a.TaskID]time.Time
}

func newArtifactStore() *artifactStore {
	return &artifactStore{
		artifacts:  make(map[a2a.TaskID][]*a2a.Artifact),
		timestamps: make(map[a2a.TaskID]time.Time),
	}
}

func (s *artifactStore) Add(taskID a2a.TaskID, artifact *a2a.Artifact) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.artifacts[taskID] = append(s.artifacts[taskID], artifact)
	s.timestamps[taskID] = time.Now()
}

func (s *artifactStore) Get(taskID a2a.TaskID) []*a2a.Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*a2a.Artifact, len(s.artifacts[taskID]))
	copy(result, s.artifacts[taskID])

	return result
}

func (s *artifactStore) Take(taskID a2a.TaskID) []*a2a.Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()

	arts := s.artifacts[taskID]
	delete(s.artifacts, taskID)
	delete(s.timestamps, taskID)

	return arts
}

func (s *artifactStore) Cleanup(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, ts := range s.timestamps {
		if ts.Before(cutoff) {
			delete(s.artifacts, id)
			delete(s.timestamps, id)
		}
	}
}
