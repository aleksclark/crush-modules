package clientserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	protocol "github.com/aleksclark/crush-modules/clientserver"
)

type acceptAuthenticator struct{}

func (acceptAuthenticator) Authenticate(context.Context, http.Header) error { return nil }

func register(t *testing.T, mux *http.ServeMux, impl protocol.Server) {
	t.Helper()
	handler, err := protocol.NewHandler(impl, protocol.RegistrationOptions{Authenticator: acceptAuthenticator{}})
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", handler)
}

func TestUnimplementedServerReturnsTypedNotImplemented(t *testing.T) {
	t.Parallel()
	_, err := (protocol.UnimplementedServer{}).Health(context.Background(), protocol.OperationHealthRequest{})
	var httpErr *protocol.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusNotImplemented {
		t.Fatalf("Health error = %#v, want typed 501", err)
	}
	_, err = (protocol.UnimplementedServer{}).SubscribeEvents(context.Background(), protocol.OperationSubscribeEventsRequest{})
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusNotImplemented {
		t.Fatalf("SubscribeEvents error = %#v, want typed 501", err)
	}
}

func TestNewHandlerOwnsProtocolMuxWithoutOverwritingOuterRoutes(t *testing.T) {
	t.Parallel()
	handler, err := protocol.NewHandler(&rejectingProbe{}, protocol.RegistrationOptions{Authenticator: acceptAuthenticator{}})
	if err != nil {
		t.Fatal(err)
	}
	outer := http.NewServeMux()
	outer.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	outer.Handle("/", handler)
	ready := httptest.NewRecorder()
	outer.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusNoContent {
		t.Fatalf("preexisting outer route status = %d, want %d", ready.Code, http.StatusNoContent)
	}
	protocolRequest := httptest.NewRecorder()
	outer.ServeHTTP(protocolRequest, httptest.NewRequest(http.MethodPost, "/v1/workspaces", bytes.NewBufferString(`{"id":"w","path":"/tmp"}`)))
	if protocolRequest.Code != http.StatusOK {
		t.Fatalf("owned protocol handler status = %d: %s", protocolRequest.Code, protocolRequest.Body.String())
	}
}

func TestNewHandlerRejectsOversizedKnownAndUnknownLengthBodiesBeforeImplementation(t *testing.T) {
	t.Parallel()
	for _, knownLength := range []bool{true, false} {
		t.Run(map[bool]string{true: "known_length", false: "chunked_unknown_length"}[knownLength], func(t *testing.T) {
			probe := &rejectingProbe{}
			handler, err := protocol.NewHandler(probe, protocol.RegistrationOptions{Authenticator: acceptAuthenticator{}, MaxBodyBytes: 16})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/workspaces", bytes.NewBufferString(`{"id":"workspace","path":"/too-large"}`))
			if !knownLength {
				req.ContentLength = -1
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want source JSON error shape", got)
			}
			var body protocol.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Message == "" {
				t.Fatalf("413 body is not source-shaped protocol.Error: %q (%v)", rec.Body.String(), err)
			}
			if probe.calls != 0 {
				t.Fatalf("implementation called %d times for oversized body", probe.calls)
			}
		})
	}
}

func TestNewHandlerRejectsNegativeBodyLimit(t *testing.T) {
	t.Parallel()
	if _, err := protocol.NewHandler(&rejectingProbe{}, protocol.RegistrationOptions{Authenticator: acceptAuthenticator{}, MaxBodyBytes: -1}); err == nil {
		t.Fatal("negative MaxBodyBytes was accepted")
	}
}

type rejectingProbe struct {
	protocol.UnimplementedServer
	calls int
}

func (p *rejectingProbe) GetWorkspaceProviders(context.Context, protocol.OperationGetWorkspaceProvidersRequest) (protocol.OperationGetWorkspaceProvidersResponse, error) {
	p.calls++
	return protocol.OperationGetWorkspaceProvidersResponse{Status: http.StatusOK}, nil
}

func (p *rejectingProbe) CreateWorkspace(_ context.Context, request protocol.CreateWorkspaceRequest) (protocol.CreateWorkspaceResponse, error) {
	p.calls++
	return protocol.CreateWorkspaceResponse{Result: request.Body}, nil
}

func TestRegisterRejectsMalformedJSONBeforeImplementation(t *testing.T) {
	t.Parallel()
	probe := &rejectingProbe{}
	mux := http.NewServeMux()
	register(t, mux, probe)
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces", bytes.NewBufferString("{"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if probe.calls != 0 {
		t.Fatalf("implementation called %d times after malformed JSON", probe.calls)
	}
}

func TestRegisterDispatchesTypedBodyAndResult(t *testing.T) {
	t.Parallel()
	probe := &rejectingProbe{}
	mux := http.NewServeMux()
	register(t, mux, probe)
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces", bytes.NewBufferString(`{"id":"w","path":"/tmp"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if probe.calls != 1 {
		t.Fatalf("implementation calls = %d, want 1", probe.calls)
	}
	var got protocol.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "w" {
		t.Fatalf("typed result ID = %q, want w", got.ID)
	}
}

type initiateAgentProbe struct {
	protocol.UnimplementedServer
	request protocol.OperationInitiateAgentProcessingRequest
	calls   int
}

func (p *initiateAgentProbe) InitiateAgentProcessing(_ context.Context, request protocol.OperationInitiateAgentProcessingRequest) (protocol.OperationInitiateAgentProcessingResponse, error) {
	p.request = request
	p.calls++
	return protocol.OperationInitiateAgentProcessingResponse{}, nil
}

func TestInitiateAgentProcessingDispatchesAgentInitRequestBody(t *testing.T) {
	t.Parallel()
	probe := &initiateAgentProbe{}
	handler, err := protocol.NewHandler(probe, protocol.RegistrationOptions{Authenticator: acceptAuthenticator{}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/workspaces/workspace/agent/init", strings.NewReader(`{"interactive":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if probe.calls != 1 {
		t.Fatalf("implementation calls = %d, want 1", probe.calls)
	}
	if !probe.request.Body.Interactive {
		t.Fatal("typed AgentInitRequest Body.Interactive = false, want true")
	}
	wire, err := json.Marshal(probe.request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip protocol.AgentInitRequest
	if err := json.Unmarshal(wire, &roundTrip); err != nil {
		t.Fatalf("round-trip AgentInitRequest: %v", err)
	}
	if !roundTrip.Interactive {
		t.Fatalf("AgentInitRequest JSON round-trip lost interactive: %s", wire)
	}
}

func TestRoutesIncludeDocsAndSSEMetadata(t *testing.T) {
	t.Parallel()
	var docs, events bool
	for _, route := range protocol.Routes {
		if route.Path == "/v1/docs/" && route.Kind == protocol.RoutePrefix && route.Classification == protocol.RouteUnsupported && route.Reason != "" {
			docs = true
		}
		if route.Path == "/v1/workspaces/{id}/events" && route.SSE && route.Auth == protocol.AuthBearer && route.Classification == protocol.RouteTypedStream {
			events = true
		}
	}
	if !docs || !events {
		t.Fatalf("docs=%v events=%v; generated registry must describe both", docs, events)
	}
}

func TestRegisterDeliberatelyRejectsEveryUnsupportedRegisteredRoute(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	register(t, mux, &rejectingProbe{})
	for _, route := range protocol.Routes {
		if route.Classification != protocol.RouteUnsupported {
			continue
		}
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
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want deliberate %d", method, path, rec.Code, http.StatusNotImplemented)
		}
	}
}

type streamProbe struct {
	protocol.UnimplementedServer
	called   chan struct{}
	producer protocol.OperationSubscribeEventsProducer
}

type streamProducer struct{ called chan struct{} }

func (p streamProducer) Serve(ctx context.Context, sink protocol.EventSink) error {
	if err := sink.Send(ctx, protocol.MessageSSEEvent{Event: protocol.Event[protocol.Message]{Type: "updated"}}); err != nil {
		return err
	}
	close(p.called)
	<-ctx.Done()
	return nil
}

func (p *streamProbe) SubscribeEvents(_ context.Context, request protocol.OperationSubscribeEventsRequest) (protocol.OperationSubscribeEventsProducer, error) {
	if request.ID != "workspace" || request.Query.ClientID != "11111111-1111-1111-1111-111111111111" {
		return nil, context.Canceled
	}
	if request.Header.Get("Authorization") != "Bearer token" {
		return nil, &protocol.HTTPError{Status: http.StatusUnauthorized}
	}
	if p.producer != nil {
		return p.producer, nil
	}
	return streamProducer{called: p.called}, nil
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
	failAt  int
}

func (r *flushRecorder) Flush() { r.flushes++ }

func (r *flushRecorder) FlushError() error {
	r.flushes++
	if r.failAt > 0 && r.flushes >= r.failAt {
		return errors.New("flush failed")
	}
	return nil
}

type flushErrorProducer struct{ done chan error }

func (p flushErrorProducer) Serve(ctx context.Context, sink protocol.EventSink) error {
	err := sink.Send(ctx, protocol.MessageSSEEvent{Event: protocol.Event[protocol.Message]{Type: "updated"}})
	p.done <- err
	return err
}

func TestSSEEventFlushErrorTerminatesProducer(t *testing.T) {
	done := make(chan error, 1)
	probe := &streamProbe{producer: flushErrorProducer{done: done}}
	mux := http.NewServeMux()
	register(t, mux, probe)
	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/workspace/events?client_id=11111111-1111-1111-1111-111111111111", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), failAt: 2}
	mux.ServeHTTP(rec, req)
	if err := <-done; err == nil || err.Error() != "flush failed" {
		t.Fatalf("producer flush error = %v", err)
	}
}

func TestRegisterStreamsTypedSSEAndCancels(t *testing.T) {
	probe := &streamProbe{called: make(chan struct{})}
	mux := http.NewServeMux()
	register(t, mux, probe)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/workspaces/workspace/events?client_id=11111111-1111-1111-1111-111111111111", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(rec, req)
		close(done)
	}()
	<-probe.called
	cancel()
	<-done
	if rec.Code != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("data: ")) || !bytes.HasSuffix(rec.Body.Bytes(), []byte("\n\n")) {
		t.Fatalf("invalid SSE framing: %q", rec.Body.String())
	}
	if rec.flushes < 2 {
		t.Fatalf("flushes = %d, want initial response and each event flushed", rec.flushes)
	}
}

func TestAllTypedSSEVariantsRoundTrip(t *testing.T) {
	t.Parallel()
	variants := []protocol.SSEEvent{
		protocol.LSPEventSSEEvent{Event: protocol.Event[protocol.LSPEvent]{}},
		protocol.MCPEventSSEEvent{Event: protocol.Event[protocol.MCPEvent]{}},
		protocol.PermissionRequestSSEEvent{Event: protocol.Event[protocol.PermissionRequest]{}},
		protocol.PermissionNotificationSSEEvent{Event: protocol.Event[protocol.PermissionNotification]{}},
		protocol.MessageSSEEvent{Event: protocol.Event[protocol.Message]{}},
		protocol.SessionSSEEvent{Event: protocol.Event[protocol.Session]{}},
		protocol.FileSSEEvent{Event: protocol.Event[protocol.File]{}},
		protocol.AgentEventSSEEvent{Event: protocol.Event[protocol.AgentEvent]{}},
		protocol.ConfigChangedSSEEvent{Event: protocol.Event[protocol.ConfigChanged]{}},
		protocol.SkillsEventSSEEvent{Event: protocol.Event[protocol.SkillsEvent]{}},
		protocol.RunCompleteSSEEvent{Event: protocol.Event[protocol.RunComplete]{}},
		protocol.UpdateAvailableSSEEvent{Event: protocol.Event[protocol.UpdateAvailable]{}},
		protocol.QuestionRequestSSEEvent{Event: protocol.Event[protocol.QuestionRequest]{}},
		protocol.QuestionNotificationSSEEvent{Event: protocol.Event[protocol.QuestionNotification]{}},
	}
	for _, want := range variants {
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal %T: %v", want, err)
		}
		var envelope protocol.EventEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("unmarshal %T: %v", want, err)
		}
		got, err := envelope.DecodeEvent()
		if err != nil {
			t.Fatalf("decode %T: %v", want, err)
		}
		if reflect.TypeOf(got) != reflect.TypeOf(want) {
			t.Fatalf("decoded type = %T, want %T", got, want)
		}
		if envelope.Type != protocol.EventType(want.EventType()) {
			t.Fatalf("wire discriminator = %q, want %q", envelope.Type, want.EventType())
		}
	}
	for _, malformed := range []string{`{"type":"unknown","payload":{}}`, `{"type":"message","payload":false}`} {
		var envelope protocol.EventEnvelope
		if err := json.Unmarshal([]byte(malformed), &envelope); err != nil {
			t.Fatal(err)
		}
		if _, err := envelope.DecodeEvent(); err == nil {
			t.Fatalf("malformed envelope %s decoded", malformed)
		}
	}
}

type controlProbe struct {
	protocol.UnimplementedServer
	shutdown, idle int
}

func (p *controlProbe) ShutdownServer(context.Context, protocol.OperationShutdownServerRequest) (protocol.OperationShutdownServerResponse, error) {
	p.shutdown++
	return protocol.OperationShutdownServerResponse{}, nil
}

func (p *controlProbe) ShutdownServerIfIdle(context.Context, protocol.OperationShutdownServerIfIdleRequest) (protocol.OperationShutdownServerIfIdleResponse, error) {
	p.idle++
	return protocol.OperationShutdownServerIfIdleResponse{}, nil
}

func TestRegisterDiscriminatesSharedControlRoute(t *testing.T) {
	t.Parallel()
	probe := &controlProbe{}
	mux := http.NewServeMux()
	register(t, mux, probe)
	for _, tc := range []struct {
		body      string
		status    int
		shutdowns int
		idles     int
	}{
		{`{`, http.StatusBadRequest, 0, 0},
		{`{"command":"shutdown"}`, http.StatusOK, 1, 0},
		{`{"command":"shutdown_if_idle"}`, http.StatusOK, 1, 1},
		{`{"command":"unknown"}`, http.StatusBadRequest, 1, 1},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/control", bytes.NewBufferString(tc.body)))
		if rec.Code != tc.status || probe.shutdown != tc.shutdowns || probe.idle != tc.idles {
			t.Fatalf("body %s: status=%d shutdown=%d idle=%d", tc.body, rec.Code, probe.shutdown, probe.idle)
		}
	}
}

func TestOperationStatusAndResponseMetadataAreSourceDerived(t *testing.T) {
	t.Parallel()
	for _, operation := range protocol.Operations {
		if operation.ResponseHeaders == nil {
			t.Fatalf("%s response headers are absent", operation.Name)
		}
		for _, header := range operation.ResponseHeaders {
			if header == "" {
				t.Fatalf("%s has an empty source-derived response header", operation.Name)
			}
		}
	}
	for _, operation := range protocol.Operations {
		if operation.Classification != "http" {
			continue
		}
		switch operation.StatusSemantics {
		case protocol.StatusSemanticsGuarded:
			if len(operation.AcceptedStatuses) == 0 {
				t.Fatalf("%s is guarded without accepted statuses", operation.Name)
			}
		case protocol.StatusSemanticsSourceUnspecified:
			if operation.AcceptedStatuses != nil {
				t.Fatalf("%s has unguarded source semantics with statuses %v", operation.Name, operation.AcceptedStatuses)
			}
		default:
			t.Fatalf("%s has unknown status semantics %q", operation.Name, operation.StatusSemantics)
		}
	}
}
