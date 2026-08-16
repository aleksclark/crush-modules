package clientserver_test

import (
	"encoding/json"
	"reflect"
	"testing"

	protocol "github.com/aleksclark/crush-modules/clientserver"
)

func TestGeneratedSSEEventsPreserveBothWireEnvelopeLayers(t *testing.T) {
	t.Parallel()
	fixtures := []protocol.SSEEvent{
		protocol.LSPEventSSEEvent{Event: protocol.Event[protocol.LSPEvent]{Type: "updated"}},
		protocol.MCPEventSSEEvent{Event: protocol.Event[protocol.MCPEvent]{Type: "updated"}},
		protocol.PermissionRequestSSEEvent{Event: protocol.Event[protocol.PermissionRequest]{Type: "created"}},
		protocol.PermissionNotificationSSEEvent{Event: protocol.Event[protocol.PermissionNotification]{Type: "updated"}},
		protocol.QuestionRequestSSEEvent{Event: protocol.Event[protocol.QuestionRequest]{Type: "created"}},
		protocol.QuestionNotificationSSEEvent{Event: protocol.Event[protocol.QuestionNotification]{Type: "updated"}},
		protocol.MessageSSEEvent{Event: protocol.Event[protocol.Message]{Type: "updated"}},
		protocol.SessionSSEEvent{Event: protocol.Event[protocol.Session]{Type: "updated"}},
		protocol.FileSSEEvent{Event: protocol.Event[protocol.File]{Type: "updated"}},
		protocol.AgentEventSSEEvent{Event: protocol.Event[protocol.AgentEvent]{Type: "updated"}},
		protocol.ConfigChangedSSEEvent{Event: protocol.Event[protocol.ConfigChanged]{Type: "updated"}},
		protocol.SkillsEventSSEEvent{Event: protocol.Event[protocol.SkillsEvent]{Type: "updated"}},
		protocol.RunCompleteSSEEvent{Event: protocol.Event[protocol.RunComplete]{Type: "updated"}},
		protocol.UpdateAvailableSSEEvent{Event: protocol.Event[protocol.UpdateAvailable]{Type: "updated"}},
	}
	for _, want := range fixtures {
		t.Run(want.EventType(), func(t *testing.T) {
			wire, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var envelope protocol.EventEnvelope
			if err := json.Unmarshal(wire, &envelope); err != nil {
				t.Fatal(err)
			}
			if string(envelope.Type) != want.EventType() {
				t.Fatalf("outer type = %q, want %q", envelope.Type, want.EventType())
			}
			var inner struct {
				Type protocol.EventType `json:"type"`
			}
			if err := json.Unmarshal(envelope.Payload, &inner); err != nil {
				t.Fatal(err)
			}
			if inner.Type == "" {
				t.Fatal("nested lifecycle event type is empty")
			}
			event, err := envelope.DecodeEvent()
			if err != nil {
				t.Fatal(err)
			}
			if reflect.TypeOf(event) != reflect.TypeOf(want) {
				t.Fatalf("decoded type = %T, want %T", event, want)
			}
			got, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			var gotJSON, wantJSON any
			if json.Unmarshal(got, &gotJSON) != nil || json.Unmarshal(wire, &wantJSON) != nil || !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Fatalf("round trip = %s, want %s", got, wire)
			}
		})
	}
}
