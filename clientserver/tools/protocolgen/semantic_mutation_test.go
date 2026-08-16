package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSemanticSourceMutations uses a committed temporary producer copy, then
// dirties exact producer declarations. It proves the test-only mutation seam
// does not relax the production CLI's clean-producer gate.
func TestSemanticSourceMutationsFollowOrFailClosed(t *testing.T) {
	base := resolvedModule()
	baseline, err := generate(base)
	if err != nil {
		t.Fatalf("generate clean producer: %v", err)
	}
	mutated := isolatedProducerCopy(t, base)

	mutateOne(t, filepath.Join(mutated.Dir, "internal", "proto", "server.go"), `ServerControlShutdown = "shutdown"`, `ServerControlShutdown = "pause"`)
	mutateOne(t, filepath.Join(mutated.Dir, "internal", "server", "server.go"), `GET /v1/workspaces/{id}/events`, `GET /v1/workspaces/{id}/event-feed`)
	mutateOne(t, filepath.Join(mutated.Dir, "internal", "client", "proto.go"), `"/workspaces/%s/events"`, `"/workspaces/%s/event-feed"`)
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "client", "proto.go"), "func (c *Client) SubscribeEvents", `"client_id": []string{c.clientID}`, `"subscriber": []string{c.clientID}`)
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "server", "proto.go"), "func (c *controllerV1) requireClientID", `Query().Get("client_id")`, `Query().Get("subscriber")`)
	mutateOne(t, filepath.Join(mutated.Dir, "internal", "server", "proto.go"), `"Cache-Control", "no-cache"`, `"Cache-Control", "must-revalidate"`)
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "server", "proto.go"), "func jsonEncode", `"Content-Type"`, `"X-Source-Type"`)
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "client", "proto.go"), "func (c *Client) SendMessage", "http.StatusAccepted", "http.StatusCreated")
	mutateOne(t, filepath.Join(mutated.Dir, "internal", "server", "proto.go"), `"data: %s\n\n"`, `"event: update\ndata: %s\n\n"`)
	mutateOne(t, filepath.Join(mutated.Dir, "internal", "proto", "proto.go"), "`json:\"message\"`", "`json:\"detail\"`")
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "server", "proto.go"), "func (c *controllerV1) handlePostControl", `jsonError(w, http.StatusBadRequest, "failed to decode request")`, `jsonError(w, http.StatusUnprocessableEntity, "malformed control")`)

	if _, err := generate(mutated); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("production generation accepted dirty mutation producer: %v", err)
	}
	artifact, err := generateForSourceMutation(mutated)
	if err != nil {
		t.Fatalf("generate isolated source mutation: %v", err)
	}
	if bytes.Equal(baseline, artifact) {
		t.Fatal("semantic source mutations produced the baseline artifact")
	}

	semantics, err := sourceSemantics(mutated)
	if err != nil {
		t.Fatalf("extract mutated source semantics: %v", err)
	}
	if !hasControl(semantics.control, "pause") {
		t.Fatalf("control discriminator did not follow source mutation: %#v", semantics.control)
	}
	if semantics.decodeError.status != 422 || semantics.decodeError.message != "malformed control" {
		t.Fatalf("malformed-control error semantics = %#v, want status=422 message=malformed control", semantics.decodeError)
	}
	if !bytes.Contains(artifact, []byte(`SourceErrorJSONField = "detail"`)) {
		t.Fatal("generated metadata did not follow the source Error JSON field")
	}
	stream := semantics.stream
	if stream == nil {
		t.Fatal("mutated producer lost SSE semantics")
	}
	if stream.route.Path != "/v1/workspaces/{id}/event-feed" || len(stream.queryRequired) != 1 || stream.queryRequired[0] != "subscriber" {
		t.Fatalf("SSE route/query semantics = %#v", stream)
	}
	if !hasHeader(stream.headers, "Cache-Control", "must-revalidate") || stream.frame != "event: update\ndata: %s\n\n" {
		t.Fatalf("SSE header/frame semantics = %#v", stream)
	}
	if !hasResponseHeader(semantics.responseHeaders["CreateWorkspace"], "X-Source-Type") {
		t.Fatalf("CreateWorkspace response headers did not follow source jsonEncode mutation: %#v", semantics.responseHeaders["CreateWorkspace"])
	}
	ops, _, err := clientOperations(mutated)
	if err != nil {
		t.Fatalf("extract mutated client operations: %v", err)
	}
	if !hasStatus(ops, "SendMessage", 201) {
		t.Fatalf("SendMessage status semantics did not follow source mutation: %#v", ops)
	}
	if !bytes.Contains(artifact, []byte(`"X-Source-Type"`)) || !bytes.Contains(artifact, []byte("AcceptedStatuses: []int{200, 201}")) {
		t.Fatal("generated operation metadata did not follow mutated source response headers/status")
	}
	if !bytes.Contains(artifact, []byte("event: update\\ndata: %s\\n\\n")) {
		t.Fatal("generated artifact does not carry extracted SSE frame")
	}
}

func TestClientIDValidationHelperMutationPropagatesOrFailsClosed(t *testing.T) {
	mutated := isolatedProducerCopy(t, resolvedModule())
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "server", "proto.go"), "func (c *controllerV1) requireClientID", `jsonError(w, http.StatusBadRequest, "client_id is not a valid UUID")`, `jsonError(w, http.StatusUnprocessableEntity, "client_id rejected by source validator")`)
	artifact, err := generateForSourceMutation(mutated)
	if err != nil {
		return // unsupported helper syntax must fail generation rather than retain stale validation semantics.
	}
	if !bytes.Contains(artifact, []byte(`writeSourceError(w, 422, "client_id rejected by source validator")`)) {
		t.Fatal("generated artifact retained stale client_id validator status/message after helper mutation")
	}
}

func TestStatusExtractionRejectsUnrecognizedGuardMutation(t *testing.T) {
	mutated := isolatedProducerCopy(t, resolvedModule())
	mutateFunctionOne(t, filepath.Join(mutated.Dir, "internal", "client", "proto.go"), "func (c *Client) SendMessage", "http.StatusAccepted", "int(http.StatusAccepted)")
	if _, err := generateForSourceMutation(mutated); err == nil || !strings.Contains(err.Error(), "unsupported SendMessage response status") {
		t.Fatalf("unrecognized status guard did not fail closed: %v", err)
	}
}

func sourceSemantics(m module) (routeIR, error) {
	routes, err := serverRoutes(m.Dir)
	if err != nil {
		return routeIR{}, err
	}
	operations, _, err := clientOperations(m)
	if err != nil {
		return routeIR{}, err
	}
	serverOperations, err := serverOnlyOperations(m, routes, operations)
	if err != nil {
		return routeIR{}, err
	}
	return sourceRouteIR(m, routes, append(operations, serverOperations...))
}

func hasControl(controls []controlIR, value string) bool {
	for _, control := range controls {
		if control.value == value {
			return true
		}
	}
	return false
}

func hasResponseHeader(headers []headerParam, name string) bool {
	for _, header := range headers {
		if header.Name == name {
			return true
		}
	}
	return false
}

func hasStatus(operations []operation, name string, status int) bool {
	for _, operation := range operations {
		if operation.Name != name {
			continue
		}
		for _, got := range operation.AcceptedStatuses {
			if got == status {
				return true
			}
		}
	}
	return false
}

func hasHeader(headers []headerParam, name, value string) bool {
	for _, header := range headers {
		if header.Name != name {
			continue
		}
		for _, candidate := range header.Values {
			if candidate == value {
				return true
			}
		}
	}
	return false
}

func isolatedProducerCopy(t *testing.T, source module) module {
	t.Helper()
	dir := t.TempDir()
	archive := exec.Command("git", "-C", source.Dir, "archive", "--format=tar", "HEAD")
	out, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	untar := exec.Command("tar", "-x", "-C", dir)
	untar.Stdin = out
	if err := archive.Start(); err != nil {
		t.Fatal(err)
	}
	if err := untar.Run(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=protocolgen-test", "-c", "user.email=protocolgen@example.test", "commit", "-qm", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(cmd.Args, " "), err, output)
		}
	}
	source.Dir = dir
	return source
}

func mutateOne(t *testing.T, path, old, new string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(content), old); count != 1 {
		t.Fatalf("%s mutation target %q count = %d, want 1", path, old, count)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(content), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mutateFunctionOne(t *testing.T, path, declaration, old, new string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(content), declaration)
	if start < 0 {
		t.Fatalf("%s declaration %q not found", path, declaration)
	}
	end := strings.Index(string(content)[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("%s declaration %q end not found", path, declaration)
	}
	end += start + len("\n}\n")
	body := string(content)[start:end]
	if count := strings.Count(body, old); count != 1 {
		t.Fatalf("%s/%s mutation target %q count = %d, want 1", path, declaration, old, count)
	}
	updated := string(content)[:start] + strings.Replace(body, old, new, 1) + string(content)[end:]
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}
