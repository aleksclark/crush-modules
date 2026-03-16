package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ParseSSEStream reads a text/event-stream response and emits typed events.
// The returned channel is closed when the stream ends or an error occurs.
// The caller should read from the channel until it closes.
func ParseSSEStream(r io.Reader) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)

		var dataLines []string
		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				if len(dataLines) > 0 {
					data := strings.Join(dataLines, "\n")
					dataLines = nil

					var event Event
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						ch <- Event{
							Type:  EventError,
							Error: &ACPError{Message: fmt.Sprintf("failed to parse SSE event: %v", err)},
						}
						continue
					}
					ch <- event
				}
				continue
			}

			if strings.HasPrefix(line, "data:") {
				value := strings.TrimPrefix(line, "data:")
				value = strings.TrimPrefix(value, " ")
				dataLines = append(dataLines, value)
				continue
			}

			// Ignore event:, id:, retry:, and comment lines per SSE spec.
		}
	}()
	return ch
}
