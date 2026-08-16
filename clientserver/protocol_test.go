package clientserver_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	protocol "github.com/aleksclark/crush-modules/clientserver"
)

type externalServer struct{ protocol.UnimplementedServer }

var _ protocol.Server = externalServer{}

func TestExternalModuleCanImplementServer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.test/consumer\n\ngo 1.26.5\n\nrequire github.com/aleksclark/crush-modules/clientserver v0.0.0\nreplace github.com/aleksclark/crush-modules/clientserver => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package consumer
import protocol "github.com/aleksclark/crush-modules/clientserver"
type server struct { protocol.UnimplementedServer }
var _ protocol.Server = server{}
`
	if err := os.WriteFile(filepath.Join(dir, "consumer.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-mod=mod", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external import failed: %v\n%s", err, out)
	}
}

func TestGeneratedWireTypesRoundTrip(t *testing.T) {
	t.Parallel()
	fixture := []byte(`{"session_id":"s","run_id":"r","message":{"role":"assistant","parts":[{"type":"text","data":{"text":"hello"}}]}}`)
	var event protocol.AgentEvent
	if err := json.Unmarshal(fixture, &event); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var again protocol.AgentEvent
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if event.RunID != again.RunID {
		t.Fatalf("run id changed: %q != %q", event.RunID, again.RunID)
	}
}

func TestGeneratedWireTypesRoundTripPreservesSecondContentPartVariant(t *testing.T) {
	t.Parallel()
	fixture := []byte(`{"session_id":"s","run_id":"r","message":{"role":"assistant","parts":[{"type":"reasoning","data":{"thinking":"because","signature":"sig"}}]}}`)
	var event protocol.AgentEvent
	if err := json.Unmarshal(fixture, &event); err != nil {
		t.Fatal(err)
	}
	if len(event.Message.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(event.Message.Parts))
	}
	part, ok := event.Message.Parts[0].(protocol.ReasoningContent)
	if !ok || part.Thinking != "because" || part.Signature != "sig" {
		t.Fatalf("decoded part = %#v, want ReasoningContent", event.Message.Parts[0])
	}
	out, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"type":"reasoning"`)) {
		t.Fatalf("round-trip lost reasoning variant: %s", out)
	}
}

func TestConfigObjectRoundTripsAsTypedWireDTO(t *testing.T) {
	t.Parallel()
	fixture := []byte(`{"models":{"large":{"model":"gpt","provider":"openai"}},"providers":{"openai":{"id":"openai","oauth":{"access_token":"token","expires_in":1,"expires_at":2}}}}`)
	var cfg protocol.Config
	if err := json.Unmarshal(fixture, &cfg); err != nil {
		t.Fatalf("decode config object: %v", err)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("config encoded as non-object JSON: %v; %s", err, encoded)
	}
	if _, ok := decoded["models"]; !ok {
		t.Fatalf("missing models after round trip: %s", encoded)
	}
}

func TestGeneratedCoverageInventory(t *testing.T) {
	t.Parallel()
	if got := len(protocol.Routes); got != 71 {
		t.Fatalf("generated route inventory = %d, want exact source registration set of 71", got)
	}
	if got := len(protocol.ClientMethods); got != 73 {
		t.Fatalf("generated client method inventory = %d, want exact exported source set of 73", got)
	}
	for _, method := range protocol.ClientMethods {
		if method.Classification == "" {
			t.Fatalf("client method %q has no mechanical classification", method.Name)
		}
	}
	if protocol.AuthorizationHeader != "Authorization" || protocol.AuthorizationScheme != "Bearer" {
		t.Fatal("missing generated bearer metadata")
	}
}

func TestEveryHTTPClientOperationHasAnExactNormalizedServerRoute(t *testing.T) {
	t.Parallel()
	routes := map[string]protocol.Route{}
	for _, route := range protocol.Routes {
		routes[route.Method+" "+normalizeRoutePath(route.Path)] = route
	}
	if got := len(protocol.ClientOperationRoutes); got != 70 {
		t.Fatalf("generated client-operation mappings = %d, want 70", got)
	}
	mapped := map[string]protocol.ClientOperationRoute{}
	for _, mapping := range protocol.ClientOperationRoutes {
		key := mapping.Name
		if _, duplicate := mapped[key]; duplicate {
			t.Fatalf("duplicate generated mapping for %s", key)
		}
		if mapping.NormalizedPath != normalizeRoutePath(mapping.ClientPath) || mapping.NormalizedPath != normalizeRoutePath(mapping.RegisteredPath) {
			t.Fatalf("%s mapping loses positional identity: %#v", mapping.Name, mapping)
		}
		mapped[key] = mapping
	}
	var operations int
	for _, method := range protocol.ClientMethods {
		if method.Classification != "http" {
			continue
		}
		operations++
		key := method.Method + " " + normalizeRoutePath(method.Path)
		route, ok := routes[key]
		if !ok {
			t.Errorf("%s has no registered route for %s", method.Name, key)
			continue
		}
		if method.Name == "SubscribeEvents" {
			if route.Classification != protocol.RouteTypedStream {
				t.Errorf("%s classification = %q, want stream", method.Name, route.Classification)
			}
		} else if route.Classification != protocol.RouteTypedPrimary {
			t.Errorf("%s classification = %q, want typed primary", method.Name, route.Classification)
		}
	}
	if operations != 70 {
		t.Fatalf("HTTP client operations = %d, want 70", operations)
	}
}

func normalizeRoutePath(path string) string {
	return routePlaceholder.ReplaceAllString(path, "{}")
}

func TestWireResponseResultsRemainDecoderEnvelopes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		response any
		field    string
		value    any
	}{
		{"GetMCPPrompt", protocol.OperationGetMCPPromptResponse{}, "Prompt", "prompt"},
		{"GetInitializePrompt", protocol.OperationGetInitializePromptResponse{}, "Prompt", "prompt"},
		{"ProjectNeedsInitialization", protocol.OperationProjectNeedsInitializationResponse{}, "NeedsInit", true},
		{"MCPAuthURL", protocol.OperationMCPAuthURLResponse{}, "AuthURL", "https://example.test/auth"},
		{"ImportCopilot", protocol.OperationImportCopilotResponse{}, "Success", true},
		{"GrantPermission", protocol.OperationGrantPermissionResponse{}, "Resolved", true},
		{"AnswerQuestionBatch", protocol.OperationAnswerQuestionBatchResponse{}, "Resolved", true},
		{"CancelQuestionBatch", protocol.OperationCancelQuestionBatchResponse{}, "Resolved", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resultType, ok := reflect.TypeOf(tc.response).FieldByName("Result")
			if !ok || resultType.Type.Kind() != reflect.Struct {
				t.Fatalf("Result = %v, want wire envelope struct", resultType.Type)
			}
			result := reflect.New(resultType.Type).Elem()
			result.FieldByName(tc.field).Set(reflect.ValueOf(tc.value))
			wire, err := json.Marshal(result.Interface())
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if _, ok := decoded[jsonField(tc.field)]; !ok {
				t.Fatalf("wire envelope %s lacks source decoder field %q: %s", tc.name, jsonField(tc.field), wire)
			}
		})
	}
}

func jsonField(field string) string {
	switch field {
	case "NeedsInit":
		return "needs_init"
	case "AuthURL":
		return "auth_url"
	default:
		return strings.ToLower(field)
	}
}

var routePlaceholder = regexp.MustCompile(`\{[^}]+\}`)

func TestClientOperationRoutesAreNotContextPlaceholders(t *testing.T) {
	t.Parallel()
	for _, operation := range protocol.ClientMethods {
		if operation.Classification == "http" && operation.Path == "/v1/{ctx}" {
			t.Fatalf("%s was extracted from ctx instead of its path argument", operation.Name)
		}
	}
}

func TestDriftGateFailsForMutation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gen := exec.Command("go", "-C", "tools/protocolgen", "run", ".", "-out", dir)
	gen.Dir = "."
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "protocol_gen.go")
	if err := os.WriteFile(path, append([]byte("// mutated\n"), mustRead(t, path)...), 0o644); err != nil {
		t.Fatal(err)
	}
	check := exec.Command("go", "-C", "tools/protocolgen", "run", ".", "-out", dir, "-check")
	check.Dir = "."
	if out, err := check.CombinedOutput(); err == nil {
		t.Fatalf("mutated artifact unexpectedly passed drift gate: %s", out)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
