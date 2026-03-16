package acp

import (
	"sync"
	"time"
)

// runStore is a thread-safe in-memory store for active ACP runs.
type runStore struct {
	mu   sync.RWMutex
	runs map[string]*runData
}

// runData tracks a single ACP run's state and event history.
type runData struct {
	mu     sync.RWMutex
	run    Run
	events []Event

	// done is closed when the run reaches a terminal state.
	done chan struct{}
	// subscribers receive new events as they are emitted.
	subscribers []chan Event
}

func newRunStore() *runStore {
	return &runStore{
		runs: make(map[string]*runData),
	}
}

func (s *runStore) create(run Run) *runData {
	rd := &runData{
		run:  run,
		done: make(chan struct{}),
	}
	s.mu.Lock()
	s.runs[run.RunID] = rd
	s.mu.Unlock()
	return rd
}

func (s *runStore) get(runID string) *runData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runs[runID]
}

func (s *runStore) delete(runID string) {
	s.mu.Lock()
	delete(s.runs, runID)
	s.mu.Unlock()
}

// cleanup removes runs older than maxAge.
func (s *runStore) cleanup(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rd := range s.runs {
		rd.mu.RLock()
		finished := rd.run.Status.IsTerminal()
		createdAt := rd.run.CreatedAt
		rd.mu.RUnlock()
		if finished && createdAt.Before(cutoff) {
			delete(s.runs, id)
		}
	}
}

func (rd *runData) getRun() Run {
	rd.mu.RLock()
	defer rd.mu.RUnlock()
	return rd.run
}

func (rd *runData) getEvents() []Event {
	rd.mu.RLock()
	defer rd.mu.RUnlock()
	result := make([]Event, len(rd.events))
	copy(result, rd.events)
	return result
}

func (rd *runData) emit(event Event) {
	rd.mu.Lock()
	rd.events = append(rd.events, event)
	subs := make([]chan Event, len(rd.subscribers))
	copy(subs, rd.subscribers)
	rd.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (rd *runData) setStatus(status RunStatus) {
	rd.mu.Lock()
	rd.run.Status = status
	if status.IsTerminal() {
		now := time.Now()
		rd.run.FinishedAt = &now
		select {
		case <-rd.done:
		default:
			close(rd.done)
		}
	}
	rd.mu.Unlock()
}

func (rd *runData) setOutput(output []Message) {
	rd.mu.Lock()
	rd.run.Output = output
	rd.mu.Unlock()
}

func (rd *runData) setError(err *ACPError) {
	rd.mu.Lock()
	rd.run.Error = err
	rd.mu.Unlock()
}

// subscribe returns a channel that receives new events. The channel is
// closed when the run reaches a terminal state.
func (rd *runData) subscribe() <-chan Event {
	ch := make(chan Event, 64)
	rd.mu.Lock()
	rd.subscribers = append(rd.subscribers, ch)
	rd.mu.Unlock()

	go func() {
		<-rd.done
		rd.mu.Lock()
		for i, sub := range rd.subscribers {
			if sub == ch {
				rd.subscribers = append(rd.subscribers[:i], rd.subscribers[i+1:]...)
				break
			}
		}
		rd.mu.Unlock()
		close(ch)
	}()

	return ch
}
