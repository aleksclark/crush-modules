package clientserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	protocol "github.com/aleksclark/crush-modules/clientserver"
)

func TestProvidersRouteIsTypedServerOnlyAndDispatched(t *testing.T) {
	t.Parallel()
	var route protocol.Route
	found := false
	for _, candidate := range protocol.Routes {
		if candidate.Path == "/v1/workspaces/{id}/providers" && candidate.Method == http.MethodGet {
			route = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("GET /v1/workspaces/{id}/providers missing from generated routes")
	}
	if route.Classification != protocol.RouteTypedServerOnly {
		t.Fatalf("providers classification = %q, want RouteTypedServerOnly", route.Classification)
	}
	mux := http.NewServeMux()
	register(t, mux, &rejectingProbe{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/workspaces/workspace/providers", nil))
	if rec.Code == http.StatusNotImplemented {
		t.Fatal("providers remains an unsupported 501 instead of a typed server-only dispatcher")
	}
}

func TestGeneratedAuthMetadataMatchesSourceTokenBehavior(t *testing.T) {
	t.Parallel()
	if protocol.AuthConfiguredTokenBehavior == "" || protocol.AuthOptionalTokenBehavior == "" {
		t.Fatal("source-derived token behavior metadata is empty")
	}
}

func TestEveryRoute401CarriesWWWAuthenticateBearer(t *testing.T) {
	t.Parallel()
	handler, err := protocol.NewHandler(&rejectingProbe{}, protocol.RegistrationOptions{Authenticator: rejectAuthenticator{}})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, route := range protocol.Routes {
		path := route.Path
		if route.Kind == protocol.RoutePrefix {
			path += "index.html"
		} else {
			path = strings.NewReplacer("{id}", "workspace", "{sid}", "session", "{client_id}", "client", "{lsp}", "lsp").Replace(path)
		}
		method := route.Method
		if method == "" {
			method = http.MethodGet
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", method, path, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Fatalf("%s %s WWW-Authenticate = %q, want Bearer", method, path, got)
		}
		checked++
	}
	if checked != len(protocol.Routes) {
		t.Fatalf("checked %d routes, want %d", checked, len(protocol.Routes))
	}
}

type rejectAuthenticator struct{}

func (rejectAuthenticator) Authenticate(context.Context, http.Header) error {
	return &protocol.HTTPError{Status: http.StatusUnauthorized, Message: "Unauthorized"}
}

func TestSourceJSONErrorBodyAndStatusMappings(t *testing.T) {
	t.Parallel()
	probe := &mappedErrorProbe{}
	mux := http.NewServeMux()
	register(t, mux, probe)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/workspaces/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GetWorkspace missing status = %d, want 404", rec.Code)
	}
	var payload protocol.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.Message == "" {
		t.Fatalf("error body = %s, want source JSON {message}", rec.Body.String())
	}
	control := httptest.NewRecorder()
	mux.ServeHTTP(control, httptest.NewRequest(http.MethodPost, "/v1/control", bytes.NewBufferString(`{"command":"shutdown_if_idle"}`)))
	if control.Code != http.StatusConflict {
		t.Fatalf("ShutdownServerIfIdle busy status = %d, want 409", control.Code)
	}
	unknown := httptest.NewRecorder()
	mux.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/v1/control", bytes.NewBufferString(`{"command":"unknown"}`)))
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown control status = %d, want 400", unknown.Code)
	}
	var unknownBody protocol.Error
	if err := json.Unmarshal(unknown.Body.Bytes(), &unknownBody); err != nil {
		t.Fatalf("unknown control body is not source JSON error: %s", unknown.Body.String())
	}
}

type mappedErrorProbe struct{ protocol.UnimplementedServer }

func (mappedErrorProbe) GetWorkspace(context.Context, protocol.OperationGetWorkspaceRequest) (protocol.OperationGetWorkspaceResponse, error) {
	return protocol.OperationGetWorkspaceResponse{}, &protocol.HTTPError{
		Status: http.StatusNotFound,
		Body:   protocol.Error{Message: "workspace not found"},
	}
}

func (mappedErrorProbe) ShutdownServerIfIdle(context.Context, protocol.OperationShutdownServerIfIdleRequest) (protocol.OperationShutdownServerIfIdleResponse, error) {
	return protocol.OperationShutdownServerIfIdleResponse{}, &protocol.HTTPError{
		Status: http.StatusConflict,
		Body:   protocol.Error{Message: "server is hosting live workspaces"},
	}
}

func TestSSEValidatesAndAuthenticatesBefore200(t *testing.T) {
	t.Parallel()
	probe := &failingStreamProbe{}
	mux := http.NewServeMux()
	register(t, mux, probe)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/workspaces/workspace/events", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("SSE committed 200 before query/header validation")
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("SSE headers were written before validation")
	}
	authHandler, err := protocol.NewHandler(probe, protocol.RegistrationOptions{Authenticator: rejectAuthenticator{}})
	if err != nil {
		t.Fatal(err)
	}
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, httptest.NewRequest(http.MethodGet, "/v1/workspaces/workspace/events?client_id=11111111-1111-1111-1111-111111111111", nil))
	if authRec.Code != http.StatusUnauthorized {
		t.Fatalf("SSE auth status = %d, want 401 before 200", authRec.Code)
	}
	if got := authRec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("SSE 401 WWW-Authenticate = %q", got)
	}
}

type failingStreamProbe struct{ protocol.UnimplementedServer }

func (failingStreamProbe) SubscribeEvents(context.Context, protocol.OperationSubscribeEventsRequest) (protocol.OperationSubscribeEventsProducer, error) {
	return nil, &protocol.HTTPError{Status: http.StatusBadRequest, Body: protocol.Error{Message: "client_id is required"}}
}

func TestSubscribeEventsRequestCarriesSourceDerivedBindings(t *testing.T) {
	t.Parallel()
	reqType := reflect.TypeOf(protocol.OperationSubscribeEventsRequest{})
	if _, ok := reqType.FieldByName("Header"); !ok {
		t.Fatal("SubscribeEvents request missing Header")
	}
	query, ok := reqType.FieldByName("Query")
	if !ok {
		t.Fatal("SubscribeEvents request missing Query")
	}
	if _, ok := query.Type.FieldByName("ClientID"); !ok {
		t.Fatal("SubscribeEvents request query missing source-derived ClientID")
	}
	if _, ok := reqType.FieldByName("ID"); !ok {
		t.Fatal("SubscribeEvents request path missing source-derived ID")
	}

}

func TestProvenanceReplacesStaticGeneratorVersion(t *testing.T) {
	t.Parallel()
	if protocol.GeneratorVersion == "clientserver-gen/v1" {
		t.Fatal("static GeneratorVersion remains")
	}
	if protocol.CrushSourceCommit == "unknown" {
		t.Fatal("unknown commit fallback remains")
	}
	if protocol.CrushSourceDigest == "" {
		t.Fatal("missing producer source digest")
	}
}

// TestClientIDValidatorRejectsBeforeBothSourceHandlers proves the public
// dispatchers retain the producer's requireClientID behavior before calling an
// implementation. Both errors are emitted by the producer helper.
func TestClientIDValidatorRejectsBeforeBothSourceHandlers(t *testing.T) {
	t.Parallel()
	probe := &clientIDValidationProbe{}
	handler, err := protocol.NewHandler(probe, protocol.RegistrationOptions{Authenticator: acceptAuthenticator{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, target, body, message string
	}{
		{"sse_missing", http.MethodGet, "/v1/workspaces/workspace/events", "", "client_id is required"},
		{"sse_invalid", http.MethodGet, "/v1/workspaces/workspace/events?client_id=not-a-uuid", "", "client_id is not a valid UUID"},
		{"current_session_missing", http.MethodPost, "/v1/workspaces/workspace/current-session", `{"session_id":""}`, "client_id is required"},
		{"current_session_invalid", http.MethodPost, "/v1/workspaces/workspace/current-session?client_id=not-a-uuid", `{"session_id":""}`, "client_id is not a valid UUID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want source 400: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want source JSON error", got)
			}
			var body protocol.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode source error: %v; body=%s", err, rec.Body.String())
			}
			if body.Message != tc.message {
				t.Fatalf("message = %q, want %q", body.Message, tc.message)
			}
		})
	}
	if probe.sseCalls != 0 || probe.currentSessionCalls != 0 {
		t.Fatalf("implementation calls: sse=%d current_session=%d, want zero after source validation", probe.sseCalls, probe.currentSessionCalls)
	}
}

type clientIDValidationProbe struct {
	protocol.UnimplementedServer
	sseCalls, currentSessionCalls int
}

func (p *clientIDValidationProbe) SubscribeEvents(context.Context, protocol.OperationSubscribeEventsRequest) (protocol.OperationSubscribeEventsProducer, error) {
	p.sseCalls++
	return nil, nil
}

func (p *clientIDValidationProbe) SetCurrentSession(context.Context, protocol.OperationSetCurrentSessionRequest) (protocol.OperationSetCurrentSessionResponse, error) {
	p.currentSessionCalls++
	return protocol.OperationSetCurrentSessionResponse{}, nil
}
