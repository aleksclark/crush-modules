package sdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
				msg := fmt.Sprintf("failed to parse event: %v", err)
				if looksLikeSSE(line) {
					msg = fmt.Sprintf("server sent SSE-formatted data instead of NDJSON (got line starting with %q) — the ACP server must use streamable HTTP (application/x-ndjson), not SSE (text/event-stream)", truncatePrefix(line, 40))
				}
				ch <- Event{
					Type:  EventError,
					Error: &ACPError{Message: msg},
				}
				continue
			}
			ch <- event
		}
	}()
	return ch
}

func looksLikeSSE(line string) bool {
	return strings.HasPrefix(line, "data:") ||
		strings.HasPrefix(line, "event:") ||
		strings.HasPrefix(line, "id:") ||
		strings.HasPrefix(line, "retry:") ||
		strings.HasPrefix(line, ":") ||
		line == "[DONE]"
}

func truncatePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
