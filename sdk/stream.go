package sdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

const contentTypeNDJSON = "application/x-ndjson"

// parseStream reads a newline-delimited JSON (NDJSON) stream and emits typed
// events. Each non-empty line is expected to be a complete JSON object.
// The returned channel is closed when the stream ends or an error occurs.
func parseStream(r io.Reader) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var event Event
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				ch <- Event{
					Type:  EventError,
					Error: &ACPError{Message: fmt.Sprintf("failed to parse event: %v", err)},
				}
				continue
			}
			ch <- event
		}
	}()
	return ch
}
