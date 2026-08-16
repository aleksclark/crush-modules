// protocolgen mechanically mirrors the resolved Crush client/server wire boundary.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ast/astutil"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const protocolGeneratorSeed = "clientserver-gen/v1"

type module struct {
	Path, Version, Dir string
	Replace            *module
}
type (
	route struct {
		Method, Path, Kind string
		Handler            string
		OriginFile         string
		OriginLine         int
	}
	clientMethod struct{ Name, Method, Path, Classification string }
	operation    struct {
		clientMethod
		BodyType, ResultType string
		BodyWire, ResultWire types.Type
		HasBody, HasResult   bool
		PathParams           []string
		QueryParams          []string
		HeaderParams         []headerParam
		AcceptedStatuses     []int
		StatusSemantics      string
		Source               string
	}
	wirePlan struct {
		modulePath string
		names      map[*types.TypeName]string
		constants  map[*types.Const]bool
		imports    map[string]string
		reserved   map[string]bool
		ordered    []*types.TypeName
		packages   map[string]*packages.Package
		indexed    map[string]*packages.Package
		types      map[string]*types.TypeName
	}
	sseVariant struct {
		discriminator string
		name          string
		payload       string
	}
	// routeIR is extracted from the producer AST. Rendering may only consume
	// these values; route-specific literals do not belong in templates.
	routeIR struct {
		decodeError         errorIR
		unknownError        errorIR
		control             []controlIR
		stream              *streamIR
		responseHeaders     map[string][]headerParam
		clientIDValidations map[string]clientIDValidationIR
	}
	errorIR struct {
		status      int
		message     string
		field       string
		contentType string
	}
	controlIR struct {
		method string
		field  string
		value  string
	}
	streamIR struct {
		route         route
		method        string
		requestType   string
		producerType  string
		headers       []headerParam
		status        int
		frame         string
		initialFlush  bool
		eventFlush    bool
		pathParams    []string
		queryRequired []string
		requestHeader []headerParam
		queryErrors   []errorIR
	}
)

func main() {
	out := flag.String("out", ".", "generated package directory")
	check := flag.Bool("check", false, "fail when generated files drift")
	flag.Parse()
	m := resolvedModule()
	b, err := generate(m)
	if err != nil {
		fail(err)
	}
	name := filepath.Join(*out, "protocol_gen.go")
	if *check {
		have, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(have, b) {
			fail(fmt.Errorf("generated protocol drift: run go generate ./..."))
		}
		return
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(name, b, 0o644); err != nil {
		fail(err)
	}
}

func resolvedModule() module {
	cmd := exec.Command("go", "list", "-m", "-json", "github.com/charmbracelet/crush")
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		fail(fmt.Errorf("resolve Crush module with go list: %w", err))
	}
	var m module
	if err := json.Unmarshal(b, &m); err != nil {
		fail(err)
	}
	if m.Replace != nil && m.Replace.Dir != "" {
		m.Dir = m.Replace.Dir
	}
	if m.Dir == "" {
		fail(fmt.Errorf("resolved Crush module has no source directory"))
	}
	return m
}

func generate(m module) ([]byte, error) { return generateWithSourceCleanliness(m, true) }

// generateForSourceMutation exercises extraction against deliberately dirty
// temporary producer trees. The CLI always uses generate and remains strict.
func generateForSourceMutation(m module) ([]byte, error) {
	return generateWithSourceCleanliness(m, false)
}

func generateWithSourceCleanliness(m module, requireCleanSource bool) ([]byte, error) {
	protoDir := filepath.Join(m.Dir, "internal", "proto")
	routes, err := serverRoutes(m.Dir)
	if err != nil {
		return nil, err
	}
	operations, methods, err := clientOperations(m)
	if err != nil {
		return nil, err
	}
	serverOperations, err := serverOnlyOperations(m, routes, operations)
	if err != nil {
		return nil, err
	}
	operations = append(operations, serverOperations...)
	sort.Slice(operations, func(i, j int) bool { return operations[i].Name < operations[j].Name })
	if err := validateOperationRoutes(routes, operations); err != nil {
		return nil, err
	}
	digest, err := treeDigest([]string{filepath.Join(m.Dir, "internal", "client"), protoDir, filepath.Join(m.Dir, "internal", "server"), filepath.Join(m.Dir, "internal", "config"), filepath.Join(m.Dir, "internal", "oauth"), filepath.Join(m.Dir, "internal", "lsp"), filepath.Join(m.Dir, "internal", "agent", "tools"), filepath.Join(m.Dir, "internal", "pubsub")})
	if err != nil {
		return nil, err
	}
	if requireCleanSource {
		if err := rejectDirtyProducer(m.Dir); err != nil { // git status --porcelain is the source cleanliness gate.
			return nil, err
		}
	}
	prov, err := computeProvenanceWithSourceCleanliness(m, requireCleanSource, filepath.Join(m.Dir, "internal", "client"), protoDir, filepath.Join(m.Dir, "internal", "server"), filepath.Join(m.Dir, "internal", "config"), filepath.Join(m.Dir, "internal", "oauth"), filepath.Join(m.Dir, "internal", "lsp"), filepath.Join(m.Dir, "internal", "agent", "tools"), filepath.Join(m.Dir, "internal", "pubsub"))
	if err != nil {
		return nil, err
	}
	authConfigured, authOptional, errorField, errorMappings, err := sourceAuthAndErrorMetadata(m)
	if err != nil {
		return nil, err
	}
	plan, err := buildWirePlan(m, operations)
	if err != nil {
		return nil, err
	}
	for i := range operations {
		if operations[i].HasBody {
			operations[i].BodyType, err = plan.typeString(operations[i].BodyWire)
			if err != nil {
				return nil, operationError(operations[i], "body", err)
			}
		}
		if operations[i].HasResult {
			operations[i].ResultType, err = plan.typeString(operations[i].ResultWire)
			if err != nil {
				return nil, operationError(operations[i], "result", err)
			}
		}
	}
	declarations, err := plan.declarations()
	if err != nil {
		return nil, err
	}
	methodDecls, err := plan.serializationMethods()
	if err != nil {
		return nil, err
	}
	variants, err := sourceSSEVariants(m, plan)
	if err != nil {
		return nil, err
	}
	semantics, err := sourceRouteIR(m, routes, operations)
	if err != nil {
		return nil, err
	}
	for _, validation := range semantics.clientIDValidations {
		if validation.parserPath == "" || validation.parserAlias == "" || validation.parserFunc == "" {
			return nil, fmt.Errorf("incomplete source client ID parser metadata")
		}
		if existing, ok := plan.imports[validation.parserPath]; ok && existing != validation.parserAlias {
			return nil, fmt.Errorf("source client ID parser import alias conflict for %s", validation.parserPath)
		}
		plan.imports[validation.parserPath] = validation.parserAlias
	}
	var body bytes.Buffer
	fmt.Fprintln(&body, "// Code generated by clientserver-gen; DO NOT EDIT.")
	fmt.Fprintln(&body, "// Source: github.com/charmbracelet/crush internal/client, internal/proto, internal/server.")
	fmt.Fprint(&body, "package clientserver\n\n")
	body.Write(plan.importDecls())
	fmt.Fprintf(&body, "\nconst GeneratorVersion = %q\nconst GeneratorDigest = %q\nconst CrushModuleVersion = %q\nconst CrushSourceCommit = %q\nconst CrushSourceDigest = %q\nconst SourceTreeDigest = %q\n", prov.GeneratorDigest, prov.GeneratorDigest, m.Version, prov.Commit, prov.SourceDigest, digest)
	fmt.Fprintf(&body, "\nconst AuthConfiguredTokenBehavior = %q\nconst AuthOptionalTokenBehavior = %q\nconst SourceErrorJSONField = %q\n", authConfigured, authOptional, errorField)
	var errorMappingNames []string
	for name := range errorMappings {
		errorMappingNames = append(errorMappingNames, name)
	}
	sort.Strings(errorMappingNames)
	for _, name := range errorMappingNames {
		fmt.Fprintf(&body, "const %s = %q\n", name, errorMappings[name])
	}
	fmt.Fprint(&body, `
// AuthorizationHeader declares the stock client/server bearer-token convention.
const AuthorizationHeader = "Authorization"
const AuthorizationScheme = "Bearer"

`)
	body.Write(declarations)
	body.Write(methodDecls)
	fmt.Fprintln(&body, "\n// Route is a stock server registration mechanically read from the production registration closure.\ntype RouteKind string\nconst ( RouteExact RouteKind = \"exact\"; RoutePrefix RouteKind = \"prefix\" )\ntype AuthKind string\nconst AuthBearer AuthKind = \"bearer\"\ntype RouteClassification string\nconst ( RouteTypedPrimary RouteClassification = \"typed_primary\"; RouteTypedStream RouteClassification = \"typed_stream\"; RouteTypedServerOnly RouteClassification = \"typed_server_only\"; RouteUnsupported RouteClassification = \"unsupported\" )\ntype Route struct { Method, Path string; Kind RouteKind; Auth AuthKind; SSE bool; Classification RouteClassification; OriginFile string; OriginLine int; Reason string }\nvar Routes = []Route{")
	for _, r := range routes {
		classification := routeClassification(r, operations)
		reason := ""
		if classification == "unsupported" {
			reason = "no exported HTTP client operation maps to this production registration"
		}
		fmt.Fprintf(&body, "{Method:%q, Path:%q, Kind:%q, Auth:AuthBearer, SSE:%t, Classification:%s, OriginFile:%q, OriginLine:%d, Reason:%q},\n", r.Method, r.Path, r.Kind, AuthRouteSSE(r), routeClassificationConstant(classification), r.OriginFile, r.OriginLine, reason)
	}
	fmt.Fprintln(&body, "}\n// ClientMethod is an exported stock client method mechanically read from internal/client.\n// Every entry is either a literal HTTP operation or an explicit non-HTTP/dynamic classification.\ntype ClientMethod struct { Name, Method, Path, Classification string }\nvar ClientMethods = []ClientMethod{")
	for _, method := range methods {
		fmt.Fprintf(&body, "{Name:%q, Method:%q, Path:%q, Classification:%q},\n", method.Name, method.Method, method.Path, method.Classification)
	}
	fmt.Fprintln(&body, "}")
	body.Write(clientOperationRouteDecls(routes, operations))
	body.Write(operationMetadataDecls(operations, semantics))
	body.Write(operationDecls(routes, operations, variants, semantics))
	return format.Source(body.Bytes())
}

func routeClassification(route route, operations []operation) string {
	if route.Method == "GET" && strings.HasSuffix(route.Handler, "Events") {
		return "typed_stream"
	}
	for _, operation := range operations {
		if operation.Classification == "http" && operation.Method == route.Method && normalizeRoutePath(operation.Path) == normalizeRoutePath(route.Path) {
			return "typed_primary"
		}
	}
	if route.Kind == "exact" && route.Method == "GET" && strings.HasSuffix(route.Handler, "Providers") {
		return "typed_server_only"
	}
	return "unsupported"
}

func routeClassificationConstant(classification string) string {
	switch classification {
	case "typed_primary":
		return "RouteTypedPrimary"
	case "typed_stream":
		return "RouteTypedStream"
	case "typed_server_only":
		return "RouteTypedServerOnly"
	case "unsupported":
		return "RouteUnsupported"
	default:
		panic("unsupported route classification: " + classification)
	}
}

func AuthRouteSSE(r route) bool {
	return strings.HasSuffix(r.Handler, "Events")
}

func normalizeRoutePath(path string) string {
	return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "{}")
}

// validateOperationRoutes is an exact operation-to-registration gate. Binding
// names intentionally remain on operations; identity compares positional path
// placeholders so `{sessionID}` and the server's `{sid}` are the same slot.
func validateOperationRoutes(routes []route, operations []operation) error {
	byIdentity := map[string][]route{}
	for _, route := range routes {
		if route.Kind != "exact" {
			continue
		}
		key := route.Method + " " + normalizeRoutePath(route.Path)
		byIdentity[key] = append(byIdentity[key], route)
	}
	for _, operation := range operations {
		if operation.Classification != "http" {
			continue
		}
		key := operation.Method + " " + normalizeRoutePath(operation.Path)
		if len(byIdentity[key]) != 1 {
			return fmt.Errorf("%s: client operation %s has %d production registrations for %s", operation.Source, operation.Name, len(byIdentity[key]), key)
		}
	}
	return nil
}

func clientOperationRouteDecls(routes []route, operations []operation) []byte {
	var out bytes.Buffer
	fmt.Fprint(&out, "\n// ClientOperationRoute is the exact positional client-to-production-route mapping.\ntype ClientOperationRoute struct { Name, Method, ClientPath, RegisteredPath, NormalizedPath string }\nvar ClientOperationRoutes = []ClientOperationRoute{\n")
	for _, operation := range operations {
		if operation.Classification != "http" {
			continue
		}
		identity := normalizeRoutePath(operation.Path)
		for _, route := range routes {
			if route.Kind == "exact" && route.Method == operation.Method && normalizeRoutePath(route.Path) == identity {
				fmt.Fprintf(&out, "{Name:%q, Method:%q, ClientPath:%q, RegisteredPath:%q, NormalizedPath:%q},\n", operation.Name, operation.Method, operation.Path, route.Path, identity)
				break
			}
		}
	}
	fmt.Fprint(&out, "}\n")
	return out.Bytes()
}

// sourceSSEVariants derives the closed stream union from SubscribeEvents' own
// discriminator switch and its concrete pubsub.Event[T] decode targets.
func sourceSSEVariants(m module, plan *wirePlan) ([]sseVariant, error) {
	client := plan.packages[m.Path+"/internal/client"]
	if client == nil {
		return nil, fmt.Errorf("load internal/client for SSE variants")
	}
	var variants []sseVariant
	seen := map[string]bool{}
	var found bool
	var walkErr error
	for _, file := range client.Syntax {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "SubscribeEvents" || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if walkErr != nil {
					return false
				}
				switchStmt, ok := node.(*ast.SwitchStmt)
				if !ok || !isPayloadTypeSwitch(switchStmt.Tag) {
					return true
				}
				found = true
				for _, statement := range switchStmt.Body.List {
					clause := statement.(*ast.CaseClause)
					if len(clause.List) == 0 {
						continue // source default rejects unknown discriminators; it is not a variant.
					}
					payload, err := sseCasePayload(client, clause)
					if err != nil {
						walkErr = err
						return false
					}
					for _, expression := range clause.List {
						constant, ok := client.TypesInfo.Uses[selectorName(expression)].(*types.Const)
						if !ok {
							walkErr = fmt.Errorf("SubscribeEvents: non-constant SSE discriminator")
							return false
						}
						discriminator, err := strconv.Unquote(constant.Val().ExactString())
						if err != nil {
							walkErr = fmt.Errorf("SubscribeEvents: decode discriminator %s: %w", constant.Name(), err)
							return false
						}
						if seen[discriminator] {
							walkErr = fmt.Errorf("SubscribeEvents: duplicate SSE discriminator %q", discriminator)
							return false
						}
						seen[discriminator] = true
						variants = append(variants, sseVariant{discriminator: discriminator, name: payload.name, payload: payload.payload})
					}
				}
				return false
			})
		}
	}
	if walkErr != nil {
		return nil, walkErr
	}
	if !found || len(variants) == 0 {
		return nil, fmt.Errorf("SubscribeEvents: no SSE variants extracted")
	}
	return variants, nil
}

type extractedSSEPayload struct{ name, payload string }

func isPayloadTypeSwitch(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Type"
}

func selectorName(expression ast.Expr) *ast.Ident {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	return selector.Sel
}

func sseCasePayload(pkg *packages.Package, clause *ast.CaseClause) (extractedSSEPayload, error) {
	for _, statement := range clause.Body {
		declaration, ok := statement.(*ast.DeclStmt)
		if !ok {
			continue
		}
		general, ok := declaration.Decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || value.Type == nil {
				continue
			}
			named, ok := pkg.TypesInfo.TypeOf(value.Type).(*types.Named)
			if !ok || named.TypeArgs().Len() != 1 {
				continue
			}
			payload, ok := named.TypeArgs().At(0).(*types.Named)
			if !ok {
				return extractedSSEPayload{}, fmt.Errorf("SubscribeEvents: unsupported SSE payload %s", named.TypeArgs().At(0))
			}
			return extractedSSEPayload{name: payload.Obj().Name(), payload: payload.Obj().Name()}, nil
		}
	}
	return extractedSSEPayload{}, fmt.Errorf("SubscribeEvents: SSE branch lacks typed event decode")
}

func operationMetadataDecls(operations []operation, semantics routeIR) []byte {
	var out bytes.Buffer
	fmt.Fprint(&out, `
// StatusSemantics records exactly how the source client checked a unary response.
type StatusSemantics string
const (
	StatusSemanticsGuarded StatusSemantics = "guarded"
	StatusSemanticsSourceUnspecified StatusSemantics = "source_unspecified"
)
// ResponseMetadata carries source-relevant HTTP response headers.
type ResponseMetadata struct { Header http.Header }
// HTTPError lets implementations, authentication, and stream setup preserve a
// source-relevant status, JSON body, and headers instead of collapsing to 500.
type HTTPError struct { Status int; Header http.Header; Body any; Message string }
func (e *HTTPError) Error() string { if e == nil { return "" }; if e.Message != "" { return e.Message }; return http.StatusText(e.Status) }
func WriteHTTPError(w http.ResponseWriter, err error) bool { var httpErr *HTTPError; if !errors.As(err,&httpErr) { return false }; if httpErr.Header != nil { for key, values := range httpErr.Header { for _, value := range values { w.Header().Add(key,value) } } }; status:=httpErr.Status; if status == 0 { status=http.StatusInternalServerError }; if httpErr.Body == nil { http.Error(w,httpErr.Error(),status); return true }; w.Header().Set("Content-Type","application/json"); w.WriteHeader(status); _=json.NewEncoder(w).Encode(httpErr.Body); return true }
// Operation is a source-derived client method contract, including status behavior.
type Operation struct {
	Name, Method, Path, Classification string
	Stream bool
	AcceptedStatuses []int
	ResponseHeaders []string
	StatusSemantics StatusSemantics
}
var Operations = []Operation{
`)
	for _, op := range operations {
		responseHeaders := semantics.responseHeaders[op.Name]
		fmt.Fprintf(&out, "{Name:%q, Method:%q, Path:%q, Classification:%q, Stream:%t, AcceptedStatuses:%s, ResponseHeaders:%s, StatusSemantics:%q},\n", op.Name, op.Method, op.Path, op.Classification, op.Name == "SubscribeEvents", intSlice(op.AcceptedStatuses), headerNames(responseHeaders), op.StatusSemantics)
	}
	fmt.Fprintln(&out, "}")
	return out.Bytes()
}

func intSlice(values []int) string {
	if values == nil {
		return "nil"
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(value)
	}
	return "[]int{" + strings.Join(parts, ",") + "}"
}

func headerNames(values []headerParam) string {
	if len(values) == 0 {
		return "[]string{}"
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Quote(value.Name)
	}
	return "[]string{" + strings.Join(parts, ",") + "}"
}

func operationDecls(routes []route, methods []operation, variants []sseVariant, semantics routeIR) []byte {
	var out bytes.Buffer
	fmt.Fprintln(&out, `
// OperationServer is the primary public boundary. Each stock HTTP client
// operation has its own request/response declaration and method.
type OperationServer interface {`)
	for _, m := range methods {
		if (m.Classification == "http" || m.Classification == "typed_server_only") && m.Name != "SubscribeEvents" {
			name := operationTypeName(m.Name)
			fmt.Fprintf(&out, "	%s(context.Context, %sRequest) (%sResponse, error)\n", m.Name, name, name)
		}
	}
	stream := semantics.stream
	fmt.Fprintf(&out, "	%s(context.Context, %sRequest) (%s, error)\n", stream.method, stream.requestType, stream.producerType)
	fmt.Fprint(&out, `}
// Authenticator owns the constant-time bearer-token policy for every route.
type Authenticator interface { Authenticate(context.Context, http.Header) error }
// RegistrationOptions is mandatory; NewHandler fails closed when Authenticator is absent.
const DefaultMaxBodyBytes int64 = 1 << 20
type RegistrationOptions struct { Authenticator Authenticator; MaxBodyBytes int64 }
// Server is the typed per-operation public protocol boundary.
type Server interface { OperationServer }
// UnimplementedServer lets a consumer satisfy the full generated interface and
// override only supported operations.
type UnimplementedServer struct{}
`)
	for _, m := range methods {
		if (m.Classification != "http" && m.Classification != "typed_server_only") || m.Name == "SubscribeEvents" {
			continue
		}
		name := operationTypeName(m.Name)
		fmt.Fprintf(&out, "func (UnimplementedServer) %s(context.Context, %sRequest) (%sResponse, error) { return %sResponse{}, &HTTPError{Status:http.StatusNotImplemented,Message:%q} }\n", m.Name, name, name, name, "operation "+m.Name+" is not implemented")
		fmt.Fprintf(&out, "type %sRequest struct { Header http.Header\n", name)
		for _, param := range m.PathParams {
			fmt.Fprintf(&out, "	%s string `json:\"-\" path:\"%s\"`\n", exportedField(param), param)
		}
		if len(m.QueryParams) > 0 {
			fmt.Fprintln(&out, "Query struct {")
			for _, query := range m.QueryParams {
				fmt.Fprintf(&out, "%s string `json:\"%s\" query:\"%s\"`\n", exportedField(query), query, query)
			}
			fmt.Fprintln(&out, "}")
		}
		if m.HasBody {
			fmt.Fprintf(&out, "Body %s `json:\"body\"`\n", m.BodyType)
		}
		fmt.Fprintln(&out, "}")
		fmt.Fprintf(&out, "type %sResponse struct { Status int; Metadata ResponseMetadata", name)
		if m.HasResult {
			fmt.Fprintf(&out, "; Result %s `json:\"result\"`", m.ResultType)
		}
		fmt.Fprintln(&out, "}")
	}
	fmt.Fprintf(&out, "type %sRequest struct { Header http.Header\n", stream.requestType)
	for _, param := range stream.pathParams {
		fmt.Fprintf(&out, "	%s string `json:\"-\" path:\"%s\"`\n", exportedField(param), param)
	}
	if len(stream.queryRequired) > 0 {
		fmt.Fprintln(&out, "Query struct {")
		for _, query := range stream.queryRequired {
			fmt.Fprintf(&out, "%s string `json:\"%s\" query:\"%s\"`\n", exportedField(query), query, query)
		}
		fmt.Fprintln(&out, "}")
	}
	fmt.Fprintln(&out, "}")
	fmt.Fprintf(&out, "// %s is opened and validated before the HTTP stream commit.\ntype %s interface { Serve(context.Context, EventSink) error }\n", stream.producerType, stream.producerType)
	fmt.Fprint(&out, "type EventSink interface { Send(context.Context,SSEEvent) error; Close(error) error }\n")
	fmt.Fprintf(&out, "func (UnimplementedServer) %s(context.Context, %sRequest) (%s, error) { return nil, &HTTPError{Status:http.StatusNotImplemented,Message:%q} }\n", stream.method, stream.requestType, stream.producerType, "operation "+stream.method+" is not implemented")
	fmt.Fprint(&out, "type sseSink struct { stateMu sync.Mutex; writeMu sync.Mutex; ctx context.Context; w http.ResponseWriter; f *http.ResponseController; frame string; closed bool }\n")
	fmt.Fprint(&out, "func (s *sseSink) Send(ctx context.Context,event SSEEvent) error { s.writeMu.Lock();defer s.writeMu.Unlock();s.stateMu.Lock();closed:=s.closed;s.stateMu.Unlock();if closed { return fmt.Errorf(\"SSE stream is closed\") }; if err:=s.ctx.Err(); err!=nil{return err};if err:=ctx.Err();err!=nil{return err};if deadline,ok:=ctx.Deadline();ok{if err:=s.f.SetWriteDeadline(deadline);err!=nil{return err};defer s.f.SetWriteDeadline(time.Time{})}; b,err:=json.Marshal(event); if err!=nil{return err}; if _,err=fmt.Fprintf(s.w,s.frame,b);err!=nil{return err}; if err:=s.f.Flush();err!=nil{return err}; return nil }\n")
	fmt.Fprint(&out, "func (s *sseSink) Close(err error) error { s.stateMu.Lock();s.closed=true;s.stateMu.Unlock();return err }\n")
	out.Write(typedDispatchDecls(routes, methods, semantics))
	fmt.Fprint(&out, `
// EventEnvelope is the stock SSE outer wire envelope. Payload is decoded only
// through DecodeEvent into one of the closed concrete SSEEvent variants.
type EventEnvelope struct { Type EventType `+"`json:\"type\"`"+`; Payload json.RawMessage `+"`json:\"payload\"`"+` }
// SSEEvent is a closed public sum type for the stock SubscribeEvents payloads.
type SSEEvent interface { sseEvent(); EventType() string }
type EventType string
type Event[T any] struct { Type EventType `+"`json:\"type\"`"+`; Payload T `+"`json:\"payload\"`"+` }
`)
	for _, v := range variants {
		fmt.Fprintf(&out, "type %sSSEEvent struct { Event[%s] }\nfunc (%sSSEEvent) sseEvent() {}\nfunc (%sSSEEvent) EventType() string { return %q }\nfunc (event %sSSEEvent) MarshalJSON() ([]byte,error) { return marshalSSEEvent(EventType(event.EventType()),event.Event) }\n", v.name, v.payload, v.name, v.name, v.discriminator, v.name)
	}
	fmt.Fprintln(&out, `
func marshalSSEEvent(outer EventType, inner any) ([]byte, error) { payload,err:=json.Marshal(inner); if err!=nil { return nil,err }; return json.Marshal(EventEnvelope{Type:outer,Payload:payload}) }
func (e EventEnvelope) DecodeEvent() (SSEEvent, error) {
	switch e.Type {`)
	for _, v := range variants {
		fmt.Fprintf(&out, "	case %q:\n		var event %sSSEEvent\n		if err := json.Unmarshal(e.Payload, &event.Event); err != nil { return nil, err }; return event, nil\n", v.discriminator, v.name)
	}
	fmt.Fprintf(&out, "%s", `	default: return nil, fmt.Errorf("unsupported SSE payload type %q", e.Type)
	}
}`)
	return out.Bytes()
}

func typedDispatchDecls(routes []route, operations []operation, semantics routeIR) []byte {
	var out bytes.Buffer
	fmt.Fprintln(&out, `
// NewHandler owns an isolated source-derived mux. Authentication is mandatory
// and applies uniformly to unary, unsupported, and SSE routes.
func NewHandler(impl Server, options RegistrationOptions) (http.Handler, error) {
	if impl == nil { return nil, fmt.Errorf("clientserver: Server is required") }
	if options.Authenticator == nil { return nil, fmt.Errorf("clientserver: RegistrationOptions.Authenticator is required") }
	maxBodyBytes,err:=bodyLimit(options); if err!=nil{return nil,err}
	private := http.NewServeMux()`)
	seen := map[string]bool{}
	shared := map[string]bool{}
	for _, variant := range semantics.control {
		for _, op := range operations {
			if op.Name == variant.method {
				shared[op.Method+" "+op.Path] = true
			}
		}
	}
	for _, op := range operations {
		if (op.Classification != "http" && op.Classification != "typed_server_only") || op.Name == "SubscribeEvents" || shared[op.Method+" "+op.Path] {
			continue
		}
		key := op.Method + " " + op.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		name := operationTypeName(op.Name)
		fmt.Fprintf(&out, "private.HandleFunc(%q, func(w http.ResponseWriter, req *http.Request) { var input %sRequest; input.Header=req.Header.Clone(); ", key, name)
		for _, param := range op.PathParams {
			field := exportedField(param)
			fmt.Fprintf(&out, "input.%s=req.PathValue(%q); if input.%s==\"\" { http.Error(w,\"missing path parameter\",http.StatusBadRequest); return }; ", field, param, field)
		}
		for _, query := range op.QueryParams {
			fmt.Fprintf(&out, "input.Query.%s=req.URL.Query().Get(%q); ", exportedField(query), query)
		}
		if validation, ok := semantics.clientIDValidations[op.Name]; ok {
			writeClientIDValidation(&out, "input.Query."+exportedField(validation.query), validation)
		}
		if op.HasBody {
			fmt.Fprintf(&out, "if !decodeRequestJSON(w,req,&input.Body,maxBodyBytes,%d,%q){return}; ", semantics.decodeError.status, semantics.decodeError.message)
		} else {
			fmt.Fprintf(&out, "if !requireEmptyRequestBody(w,req,maxBodyBytes,%d,%q){return}; ", semantics.decodeError.status, semantics.decodeError.message)
		}
		fmt.Fprintf(&out, "output,err:=impl.%s(req.Context(),input); if err!=nil { if !WriteHTTPError(w,err) { writeSourceError(w,http.StatusInternalServerError,err.Error()) }; return }; ", op.Name)
		if op.HasResult {
			fmt.Fprintf(&out, "writeJSON(w,output.Status,output.Metadata,%s,output.Result) })\n", intSlice(op.AcceptedStatuses))
		} else {
			fmt.Fprintf(&out, "writeEmpty(w,output.Status,output.Metadata,%s) })\n", intSlice(op.AcceptedStatuses))
		}
	}
	out.Write(controlDispatchDecls(operations, semantics))
	out.Write(streamDispatchDecls(operations, semantics))
	for _, route := range routes {
		if routeClassification(route, operations) != "unsupported" {
			continue
		}
		reason := fmt.Sprintf("unsupported registered route: no exported HTTP client operation maps to %s:%d", route.OriginFile, route.OriginLine)
		if route.Kind == "prefix" {
			fmt.Fprintf(&out, "private.Handle(%q,http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { http.Error(w,%q,http.StatusNotImplemented) }))\n", route.Path, reason)
		} else {
			fmt.Fprintf(&out, "private.HandleFunc(%q,func(w http.ResponseWriter, req *http.Request) { http.Error(w,%q,http.StatusNotImplemented) })\n", route.Method+" "+route.Path, reason)
		}
	}
	fmt.Fprint(&out, `
	return authorize(options, private), nil
}
func bodyLimit(options RegistrationOptions) (int64,error) { if options.MaxBodyBytes==0{return DefaultMaxBodyBytes,nil};if options.MaxBodyBytes<0{return 0,fmt.Errorf("clientserver: MaxBodyBytes must be positive")};return options.MaxBodyBytes,nil }
func authorize(options RegistrationOptions, next http.Handler) http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { if err:=options.Authenticator.Authenticate(req.Context(),req.Header.Clone()); err!=nil { var httpErr *HTTPError; if errors.As(err,&httpErr) && httpErr.Status==http.StatusUnauthorized && (httpErr.Header==nil || httpErr.Header.Get("WWW-Authenticate")=="") { w.Header().Set("WWW-Authenticate","Bearer") }; if !WriteHTTPError(w,err) { w.Header().Set("WWW-Authenticate","Bearer"); http.Error(w,err.Error(),http.StatusUnauthorized) }; return }; next.ServeHTTP(w,req) }) }
func allowQuery(values map[string][]string, allowed []string) bool { for key := range values { valid:=false; for _, allowedKey := range allowed { if key==allowedKey { valid=true; break } }; if !valid { return false } }; return true }
var errRequestBodyTooLarge=errors.New("request body too large")
const requestBodyTooLargeMessage="request body too large"
func decodeRequestJSON(w http.ResponseWriter,req *http.Request,dst any,max int64,sourceStatus int,sourceMessage string) bool { if err:=decodeJSON(w,req,dst,max);err!=nil { if errors.Is(err,errRequestBodyTooLarge){writeSourceError(w,http.StatusRequestEntityTooLarge,requestBodyTooLargeMessage)}else{writeSourceError(w,sourceStatus,sourceMessage)};return false };return true }
func decodeJSON(w http.ResponseWriter,req *http.Request,dst any,max int64) error { if req.Body==nil || req.ContentLength==0 { return fmt.Errorf("missing request body") }; if req.ContentLength>max{return errRequestBodyTooLarge}; body:=http.MaxBytesReader(w,req.Body,max);decoder:=json.NewDecoder(body);decoder.DisallowUnknownFields();if err:=decoder.Decode(dst);err!=nil{var maxErr *http.MaxBytesError;if errors.As(err,&maxErr){return errRequestBodyTooLarge};return fmt.Errorf("failed to decode request: %w",err)};var extra any;if err:=decoder.Decode(&extra);err!=io.EOF{var maxErr *http.MaxBytesError;if errors.As(err,&maxErr){return errRequestBodyTooLarge};return fmt.Errorf("failed to decode request")};return nil }
func requireEmptyRequestBody(w http.ResponseWriter,req *http.Request,max int64,sourceStatus int,sourceMessage string) bool { if req.Body==nil||req.ContentLength==0{return true};body:=http.MaxBytesReader(w,req.Body,max);data,err:=io.ReadAll(body);if err!=nil {var maxErr *http.MaxBytesError;if errors.As(err,&maxErr){writeSourceError(w,http.StatusRequestEntityTooLarge,requestBodyTooLargeMessage);return false};writeSourceError(w,sourceStatus,sourceMessage);return false};if len(data)!=0 {writeSourceError(w,sourceStatus,sourceMessage);return false};return true }
func requireEmptyBody(req *http.Request) error { if req.Body!=nil && req.ContentLength!=0 { return fmt.Errorf("request body is not accepted") }; return nil }
func defaultStatus(status int, accepted []int) int { if status != 0 { return status }; if len(accepted) != 0 { return accepted[0] }; return http.StatusOK }
func applyResponseMetadata(w http.ResponseWriter, metadata ResponseMetadata) { if metadata.Header != nil { for key, values := range metadata.Header { for _, value := range values { w.Header().Add(key,value) } } } }
func writeEmpty(w http.ResponseWriter, status int, metadata ResponseMetadata, accepted []int) { applyResponseMetadata(w,metadata); w.WriteHeader(defaultStatus(status,accepted)) }
func writeJSON(w http.ResponseWriter, status int, metadata ResponseMetadata, accepted []int, value any) { applyResponseMetadata(w,metadata); w.Header().Set("Content-Type","application/json"); w.WriteHeader(defaultStatus(status,accepted)); _=json.NewEncoder(w).Encode(value) }
`)
	fmt.Fprintf(&out, "func writeSourceError(w http.ResponseWriter,status int,message string) { w.Header().Set(\"Content-Type\",%q); w.WriteHeader(status); _=json.NewEncoder(w).Encode(Error{%s:message}) }\n", semantics.decodeError.contentType, semantics.decodeError.field)
	return out.Bytes()
}

func exportedField(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '.' || r == '-' || r == '/' })
	var out string
	for _, part := range parts {
		if part == "" {
			continue
		}
		switch strings.ToLower(part) {
		case "id":
			out += "ID"
		case "sid":
			out += "SID"
		case "lsp":
			out += "LSP"
		default:
			out += strings.ToUpper(part[:1]) + part[1:]
		}
	}
	if out == "" {
		return "Value"
	}
	return out
}

func operationTypeName(name string) string {
	if name == "CreateWorkspace" {
		return name
	}
	return "Operation" + name
}

func operationError(op operation, part string, err error) error {
	return fmt.Errorf("%s: unsupported %s %s: %w", op.Source, op.Name, part, err)
}

func buildWirePlan(m module, ops []operation) (*wirePlan, error) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps, Dir: m.Dir}
	loaded, err := packages.Load(cfg,
		"./internal/client", "./internal/proto", "./internal/config", "./internal/oauth", "./internal/lsp", "./internal/agent/tools", "./internal/pubsub")
	if err != nil || packages.PrintErrors(loaded) > 0 {
		return nil, fmt.Errorf("load Crush wire packages: %w", err)
	}
	plan := &wirePlan{
		modulePath: m.Path,
		names:      map[*types.TypeName]string{},
		constants:  map[*types.Const]bool{},
		imports:    map[string]string{},
		reserved:   map[string]bool{"Tool": true, "Event": true, "EventType": true},
		packages:   map[string]*packages.Package{},
		indexed:    map[string]*packages.Package{},
		types:      map[string]*types.TypeName{},
	}
	var indexPackage func(*packages.Package)
	indexPackage = func(pkg *packages.Package) {
		if pkg == nil || plan.indexed[pkg.PkgPath] != nil {
			return
		}
		plan.indexed[pkg.PkgPath] = pkg
		if pkg.Types != nil {
			for _, name := range pkg.Types.Scope().Names() {
				if obj, ok := pkg.Types.Scope().Lookup(name).(*types.TypeName); ok {
					plan.types[pkg.PkgPath+"."+name] = obj
				}
			}
		}
		for _, imported := range pkg.Imports {
			indexPackage(imported)
		}
	}
	for _, pkg := range loaded {
		plan.packages[pkg.PkgPath] = pkg
		indexPackage(pkg)
	}
	var roots []types.Type
	for _, op := range ops {
		if op.HasBody {
			roots = append(roots, op.BodyWire)
		}
		if op.HasResult {
			roots = append(roots, op.ResultWire)
		}
	}
	proto := plan.packages[m.Path+"/internal/proto"]
	if proto == nil {
		return nil, fmt.Errorf("load internal/proto")
	}
	for _, name := range proto.Types.Scope().Names() {
		if !strings.HasSuffix(name, "Event") && !strings.HasSuffix(name, "Request") && !strings.HasSuffix(name, "Notification") {
			continue
		}
		if obj, ok := proto.Types.Scope().Lookup(name).(*types.TypeName); ok {
			roots = append(roots, obj.Type())
		}
	}
	for _, name := range proto.Types.Scope().Names() {
		if strings.HasPrefix(name, "ServerControl") {
			if constant, ok := proto.Types.Scope().Lookup(name).(*types.Const); ok {
				plan.constants[constant] = true
			}
		}
	}
	if client := plan.packages[m.Path+"/internal/client"]; client != nil {
		for _, file := range client.Syntax {
			ast.Inspect(file, func(node ast.Node) bool {
				value, ok := node.(*ast.ValueSpec)
				if !ok || value.Type == nil {
					return true
				}
				named, ok := client.TypesInfo.TypeOf(value.Type).(*types.Named)
				if ok && named.TypeArgs().Len() == 1 {
					if strings.HasSuffix(named.Obj().Name(), "Event") || named.Obj().Name() == "Event" {
						roots = append(roots, named.TypeArgs().At(0))
					}
				}
				return true
			})
		}
	}
	if event := plan.packageType(m.Path+"/internal/pubsub", "Event"); event != nil {
		roots = append(roots, event.Type())
	}

	seen := map[types.Type]bool{}
	var visit func(types.Type) error
	visit = func(t types.Type) error {
		if t == nil || seen[t] {
			return nil
		}
		seen[t] = true
		switch x := t.(type) {
		case *types.Named:
			obj := plan.namedObject(x)
			if obj.Pkg() != nil && obj.Pkg().Path() == m.Path+"/internal/csync" && obj.Name() == "Map" {
				for i := 0; i < x.TypeArgs().Len(); i++ {
					if err := visit(x.TypeArgs().At(i)); err != nil {
						return err
					}
				}
				return nil
			}
			if plan.internal(obj) {
				plan.add(obj)
			}
			for i := 0; i < x.TypeArgs().Len(); i++ {
				if err := visit(x.TypeArgs().At(i)); err != nil {
					return err
				}
			}
			return visit(x.Underlying())
		case *types.Alias:
			obj := plan.typeObject(x.Obj())
			if plan.internal(obj) {
				plan.add(obj)
			}
			return visit(types.Unalias(x))
		case *types.Pointer:
			return visit(x.Elem())
		case *types.Slice:
			return visit(x.Elem())
		case *types.Array:
			return visit(x.Elem())
		case *types.Chan:
			return visit(x.Elem())
		case *types.Map:
			if err := visit(x.Key()); err != nil {
				return err
			}
			return visit(x.Elem())
		case *types.Struct:
			for i := 0; i < x.NumFields(); i++ {
				if err := visit(x.Field(i).Type()); err != nil {
					return err
				}
			}
		case *types.Interface:
			for i := 0; i < x.NumMethods(); i++ {
				if err := visit(x.Method(i).Type()); err != nil {
					return err
				}
			}
		case *types.Signature:
			if x.Params() != nil {
				for i := 0; i < x.Params().Len(); i++ {
					if err := visit(x.Params().At(i).Type()); err != nil {
						return err
					}
				}
			}
			if x.Results() != nil {
				for i := 0; i < x.Results().Len(); i++ {
					if err := visit(x.Results().At(i).Type()); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	if err := plan.collectSumVariants(visit); err != nil {
		return nil, err
	}
	if err := plan.collectSerializationTypes(visit); err != nil {
		return nil, err
	}
	if err := plan.collectSumVariants(visit); err != nil {
		return nil, err
	}
	plan.finalizeNames()
	for _, obj := range plan.ordered {
		if _, err := plan.typeString(obj.Type()); err != nil {
			return nil, fmt.Errorf("wire collection %s: %w", plan.source(obj), err)
		}
	}
	sort.Slice(plan.ordered, func(i, j int) bool {
		a, b := plan.ordered[i], plan.ordered[j]
		return a.Pkg().Path()+":"+a.Name() < b.Pkg().Path()+":"+b.Name()
	})
	return plan, nil
}

func (p *wirePlan) packageType(path, name string) *types.TypeName {
	return p.types[path+"."+name]
}

func (p *wirePlan) typeObject(obj *types.TypeName) *types.TypeName {
	if obj == nil || obj.Pkg() == nil {
		return obj
	}
	if indexed := p.packageType(obj.Pkg().Path(), obj.Name()); indexed != nil {
		return indexed
	}
	return obj
}

func (p *wirePlan) namedObject(named *types.Named) *types.TypeName {
	if origin := named.Origin(); origin != nil {
		return p.typeObject(origin.Obj())
	}
	return p.typeObject(named.Obj())
}

func (p *wirePlan) source(obj *types.TypeName) string {
	if obj == nil || obj.Pkg() == nil {
		return "unknown source"
	}
	if pkg := p.indexed[obj.Pkg().Path()]; pkg != nil && pkg.Fset != nil {
		return pkg.Fset.Position(obj.Pos()).String()
	}
	return obj.String()
}

func (p *wirePlan) internal(obj *types.TypeName) bool {
	if obj == nil || obj.Pkg() == nil || !strings.HasPrefix(obj.Pkg().Path(), p.modulePath+"/internal/") {
		return false
	}
	pkg := p.indexed[obj.Pkg().Path()]
	return pkg != nil && obj.Parent() == pkg.Types.Scope()
}
func (p *wirePlan) add(obj *types.TypeName) {
	if _, ok := p.names[obj]; ok {
		return
	}
	p.names[obj] = ""
	p.ordered = append(p.ordered, obj)
}

// finalizeNames projects source declarations only after the wire closure is known.
// A bare source name is retained only when it is globally unambiguous.
func (p *wirePlan) finalizeNames() {
	groups := map[string][]*types.TypeName{}
	for obj := range p.names {
		groups[obj.Name()] = append(groups[obj.Name()], obj)
	}
	for base, objects := range groups {
		sort.Slice(objects, func(i, j int) bool { return objects[i].Pkg().Path() < objects[j].Pkg().Path() })
		if len(objects) == 1 && !p.reserved[base] {
			p.names[objects[0]] = base
			continue
		}
		used := map[string]bool{}
		for _, obj := range objects {
			name := exportedField(strings.TrimPrefix(obj.Pkg().Path(), p.modulePath+"/internal/")) + base
			if used[name] {
				digest := sha256.Sum256([]byte(obj.Pkg().Path()))
				name += strings.ToUpper(hex.EncodeToString(digest[:3]))
			}
			used[name] = true
			p.names[obj] = name
		}
	}
}

// collectSumVariants follows source type-switch cases for collected interface
// declarations. It makes interface JSON sums a property of source code, not a
// handwritten generated variant list.
func (p *wirePlan) collectSumVariants(visit func(types.Type) error) error {
	var paths []string
	for path := range p.packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pkg := p.packages[path]
		for _, file := range pkg.Syntax {
			var err error
			ast.Inspect(file, func(node ast.Node) bool {
				if err != nil {
					return false
				}
				switchStmt, ok := node.(*ast.TypeSwitchStmt)
				if !ok {
					return true
				}
				var asserted ast.Expr
				switch assign := switchStmt.Assign.(type) {
				case *ast.ExprStmt:
					if assertion, ok := assign.X.(*ast.TypeAssertExpr); ok {
						asserted = assertion.X
					}
				case *ast.AssignStmt:
					if len(assign.Rhs) == 1 {
						if assertion, ok := assign.Rhs[0].(*ast.TypeAssertExpr); ok {
							asserted = assertion.X
						}
					}
				}
				iface, ok := pkg.TypesInfo.TypeOf(asserted).(*types.Named)
				if !ok || !p.internal(p.namedObject(iface)) {
					return true
				}
				if _, collected := p.names[p.namedObject(iface)]; !collected {
					return true
				}
				for _, clauseStmt := range switchStmt.Body.List {
					clause := clauseStmt.(*ast.CaseClause)
					for _, expr := range clause.List {
						if typ := pkg.TypesInfo.TypeOf(expr); typ != nil {
							err = visit(typ)
							if err != nil {
								return false
							}
						}
					}
				}
				return true
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *wirePlan) typeString(t types.Type) (string, error) {
	switch x := t.(type) {
	case *types.Basic:
		return x.Name(), nil
	case *types.TypeParam:
		return x.Obj().Name(), nil
	case *types.Named:
		obj := p.namedObject(x)
		if obj.Pkg() != nil && obj.Pkg().Path() == p.modulePath+"/internal/csync" && obj.Name() == "Map" {
			if x.TypeArgs().Len() != 2 {
				return "", fmt.Errorf("csync.Map type arguments")
			}
			key, err := p.typeString(x.TypeArgs().At(0))
			if err != nil {
				return "", err
			}
			value, err := p.typeString(x.TypeArgs().At(1))
			if err != nil {
				return "", err
			}
			return "map[" + key + "]" + value, nil
		}
		if p.internal(obj) {
			if n, ok := p.names[obj]; ok {
				if x.TypeArgs().Len() == 0 {
					return n, nil
				}
				var args []string
				for i := 0; i < x.TypeArgs().Len(); i++ {
					arg, err := p.typeString(x.TypeArgs().At(i))
					if err != nil {
						return "", err
					}
					args = append(args, arg)
				}
				return n + "[" + strings.Join(args, ",") + "]", nil
			}
			return "", fmt.Errorf("uncollected internal type %s", obj)
		}
		return types.TypeString(x, p.importName), nil
	case *types.Alias:
		obj := p.typeObject(x.Obj())
		if p.internal(obj) {
			if n, ok := p.names[obj]; ok {
				return n, nil
			}
			return "", fmt.Errorf("uncollected internal alias %s", x.Obj())
		}
		return p.typeString(types.Unalias(x))
	case *types.Pointer:
		e, err := p.typeString(x.Elem())
		return "*" + e, err
	case *types.Slice:
		e, err := p.typeString(x.Elem())
		return "[]" + e, err
	case *types.Array:
		e, err := p.typeString(x.Elem())
		return fmt.Sprintf("[%d]%s", x.Len(), e), err
	case *types.Chan:
		e, err := p.typeString(x.Elem())
		if err != nil {
			return "", err
		}
		switch x.Dir() {
		case types.SendRecv:
			return "chan " + e, nil
		case types.SendOnly:
			return "chan<- " + e, nil
		default:
			return "<-chan " + e, nil
		}
	case *types.Map:
		k, err := p.typeString(x.Key())
		if err != nil {
			return "", err
		}
		e, err := p.typeString(x.Elem())
		return "map[" + k + "]" + e, err
	case *types.Signature:
		s, err := p.signatureString(x)
		if err != nil {
			return "", err
		}
		return "func" + s, nil
	case *types.Struct:
		var b strings.Builder
		b.WriteString("struct {")
		for i := 0; i < x.NumFields(); i++ {
			f := x.Field(i)
			typ, err := p.typeString(f.Type())
			if err != nil {
				return "", err
			}
			if f.Embedded() && !strings.HasPrefix(typ, "*map[") {
				b.WriteString(" ")
			} else {
				b.WriteString(" " + f.Name() + " ")
			}
			b.WriteString(typ)
			if tag := x.Tag(i); tag != "" {
				b.WriteString(" `" + tag + "`")
			}
			b.WriteString(";")
		}
		b.WriteString(" }")
		return b.String(), nil
	case *types.Interface:
		if x.NumMethods() == 0 {
			return "any", nil
		}
		var methods []string
		for i := 0; i < x.NumMethods(); i++ {
			sig, ok := x.Method(i).Type().(*types.Signature)
			if !ok {
				return "", fmt.Errorf("interface method %s", x.Method(i).Name())
			}
			rendered, err := p.signatureString(sig)
			if err != nil {
				return "", err
			}
			methods = append(methods, x.Method(i).Name()+rendered)
		}
		return "interface { " + strings.Join(methods, "; ") + " }", nil
	default:
		return "", fmt.Errorf("type form %T", t)
	}
}

var runtimeImportAliases = map[string]string{
	"charm.land/catwalk/pkg/catwalk": "catwalk",
	"context":                        "context",
	"encoding/base64":                "base64",
	"encoding/json":                  "json",
	"errors":                         "errors",
	"fmt":                            "fmt",
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol": "protocol",
	"io":       "io",
	"net/http": "http",
	"sync":     "sync",

	"time": "time",
}

func (p *wirePlan) importName(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	if name, ok := runtimeImportAliases[pkg.Path()]; ok {
		return name
	}
	if name, ok := p.imports[pkg.Path()]; ok {
		return name
	}
	name := strings.NewReplacer(".", "_", "-", "_", "/", "_").Replace(pkg.Path())
	if name == "" {
		name = pkg.Name()
	}
	p.imports[pkg.Path()] = name
	return name
}

func (p *wirePlan) importDecls() []byte {
	imports := map[string]string{}
	for path, name := range runtimeImportAliases {
		imports[path] = name
	}
	for path, name := range p.imports {
		imports[path] = name
	}
	paths := make([]string, 0, len(imports))
	for path := range imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var out bytes.Buffer
	fmt.Fprintln(&out, "import (")
	for _, path := range paths {
		fmt.Fprintf(&out, "	%s %q\n", imports[path], path)
	}
	fmt.Fprintln(&out, ")")
	return out.Bytes()
}

func (p *wirePlan) signatureString(sig *types.Signature) (string, error) {
	var params []string
	for i := 0; i < sig.Params().Len(); i++ {
		typ, err := p.typeString(sig.Params().At(i).Type())
		if err != nil {
			return "", err
		}
		if sig.Variadic() && i == sig.Params().Len()-1 {
			typ = "..." + strings.TrimPrefix(typ, "[]")
		}
		params = append(params, typ)
	}
	var results []string
	for i := 0; i < sig.Results().Len(); i++ {
		typ, err := p.typeString(sig.Results().At(i).Type())
		if err != nil {
			return "", err
		}
		results = append(results, typ)
	}
	out := "(" + strings.Join(params, ",") + ")"
	if len(results) == 1 {
		return out + " " + results[0], nil
	}
	if len(results) > 1 {
		return out + " (" + strings.Join(results, ",") + ")", nil
	}
	return out, nil
}

func (p *wirePlan) declarations() ([]byte, error) {
	var b bytes.Buffer
	for _, obj := range p.ordered {
		name := p.names[obj]
		if obj.IsAlias() {
			rhs, err := p.typeString(types.Unalias(obj.Type()))
			if err != nil {
				return nil, fmt.Errorf("emit %s: %w", p.source(obj), err)
			}
			fmt.Fprintf(&b, "type %s = %s\n", name, rhs)
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			return nil, fmt.Errorf("emit %s: expected named type", p.source(obj))
		}
		rhs, err := p.typeString(named.Underlying())
		if err != nil {
			return nil, fmt.Errorf("emit %s: %w", p.source(obj), err)
		}
		params := ""
		if tp := named.TypeParams(); tp != nil && tp.Len() > 0 {
			var values []string
			for i := 0; i < tp.Len(); i++ {
				constraint, err := p.typeString(tp.At(i).Constraint())
				if err != nil {
					return nil, fmt.Errorf("emit %s: %w", p.source(obj), err)
				}
				values = append(values, tp.At(i).Obj().Name()+" "+constraint)
			}
			params = "[" + strings.Join(values, ",") + "]"
		}
		fmt.Fprintf(&b, "type %s%s %s\n", name, params, rhs)
	}
	// Preserve the source enum values for every emitted defined type.
	emitted := map[*types.Const]bool{}
	var packagePaths []string
	for path := range p.packages {
		packagePaths = append(packagePaths, path)
	}
	sort.Strings(packagePaths)
	for _, path := range packagePaths {
		pkg := p.packages[path]
		for _, name := range pkg.Types.Scope().Names() {
			obj, ok := pkg.Types.Scope().Lookup(name).(*types.Const)
			if !ok {
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok || !p.internal(named.Obj()) {
				continue
			}
			public, ok := p.names[named.Obj()]
			if !ok {
				continue
			}
			if _, skip := p.constants[obj]; skip {
				continue
			}
			emitted[obj] = true
			fmt.Fprintf(&b, "const %s %s = %s\n", obj.Name(), public, obj.Val().String())
		}
	}
	var selected []*types.Const
	for object := range p.constants {
		if !emitted[object] {
			selected = append(selected, object)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Pkg().Path()+":"+selected[i].Name() < selected[j].Pkg().Path()+":"+selected[j].Name()
	})
	for _, object := range selected {
		fmt.Fprintf(&b, "const %s = %s\n", object.Name(), object.Val().ExactString())
	}
	return b.Bytes(), nil
}

// collectSerializationTypes closes over declarations used by selected source wire codecs.
func (p *wirePlan) collectSerializationTypes(visit func(types.Type) error) error {
	for _, pkg := range p.packages {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !p.isSerializationFunc(pkg, fn) {
					continue
				}
				var err error
				ast.Inspect(fn, func(node ast.Node) bool {
					if err != nil {
						return false
					}
					ident, ok := node.(*ast.Ident)
					if !ok {
						return true
					}
					switch object := pkg.TypesInfo.Uses[ident].(type) {
					case *types.TypeName:
						if p.internal(p.typeObject(object)) {
							err = visit(object.Type())
						}
					case *types.Const:
						if object.Pkg() != nil && strings.HasPrefix(object.Pkg().Path(), p.modulePath+"/internal/") {
							p.constants[object] = true
						}
					case *types.PkgName:
						p.importName(object.Imported())
					}
					return err == nil
				})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func isCodecHelper(name string) bool {
	for _, candidate := range []string{"MarshalParts", "UnmarshalParts", "unmarshalToolParams"} {
		if name == candidate {
			return true
		}
	}
	return false
}

func (p *wirePlan) isSerializationFunc(pkg *packages.Package, fn *ast.FuncDecl) bool {
	if isCodecHelper(fn.Name.Name) {
		return true
	}
	object, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
	if !ok || fn.Recv == nil {
		return false
	}
	signature, ok := object.Type().(*types.Signature)
	if !ok {
		return false
	}
	receiver := signature.Recv().Type()
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	named, ok := receiver.(*types.Named)
	if !ok || !p.internal(p.namedObject(named)) {
		return false
	}
	if _, collected := p.names[p.namedObject(named)]; !collected {
		return false
	}
	switch fn.Name.Name {
	case "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText", "String", "isPart":
		return true
	default:
		return false
	}
}

func (p *wirePlan) rewriteSerializationFunc(pkg *packages.Package, fn *ast.FuncDecl) {
	astutil.Apply(fn, func(cursor *astutil.Cursor) bool {
		switch node := cursor.Node().(type) {
		case *ast.SelectorExpr:
			object, ok := pkg.TypesInfo.Uses[node.Sel].(*types.TypeName)
			if !ok {
				return true
			}
			object = p.typeObject(object)
			if p.internal(object) {
				if name, ok := p.names[object]; ok && name != node.Sel.Name {
					cursor.Replace(ast.NewIdent(name))
					return false
				}
			}
		case *ast.Ident:
			object, ok := pkg.TypesInfo.Uses[node].(*types.TypeName)
			if !ok {
				return true
			}
			object = p.typeObject(object)
			if p.internal(object) {
				if name, ok := p.names[object]; ok && name != node.Name {
					node.Name = name
				}
			}
		}
		return true
	}, nil)
}

// serializationMethods retains only source-declared wire marshaling and tagged-union
// methods. Selection is by go/types receiver identity, not file/name text matching.
func (p *wirePlan) serializationMethods() ([]byte, error) {
	allowed := map[string]bool{"MarshalJSON": true, "UnmarshalJSON": true, "MarshalText": true, "UnmarshalText": true, "String": true, "isPart": true}
	var selected []struct {
		pkg *packages.Package
		fn  *ast.FuncDecl
	}
	for _, pkg := range p.packages {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				obj, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
				if !ok {
					continue
				}
				sig, ok := obj.Type().(*types.Signature)
				if !ok {
					continue
				}
				keep := isCodecHelper(fn.Name.Name)
				if recv := sig.Recv(); recv != nil && allowed[fn.Name.Name] {
					t := recv.Type()
					if ptr, ok := t.(*types.Pointer); ok {
						t = ptr.Elem()
					}
					if named, ok := t.(*types.Named); ok && p.internal(named.Obj()) {
						_, keep = p.names[named.Obj()]
					}
				}
				if keep {
					selected = append(selected, struct {
						pkg *packages.Package
						fn  *ast.FuncDecl
					}{pkg, fn})
				}
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		left := selected[i].pkg.PkgPath + ":" + selected[i].pkg.Fset.Position(selected[i].fn.Pos()).String() + ":" + selected[i].fn.Name.Name
		right := selected[j].pkg.PkgPath + ":" + selected[j].pkg.Fset.Position(selected[j].fn.Pos()).String() + ":" + selected[j].fn.Name.Name
		return left < right
	})
	var out bytes.Buffer
	for _, item := range selected {
		p.rewriteSerializationFunc(item.pkg, item.fn)
		if err := format.Node(&out, item.pkg.Fset, item.fn); err != nil {
			return nil, err
		}
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// serverRoutes reads only the production Server.installHandler registration
// closure. It is deliberately strict: a registration that moves away from the
// tracked mux, uses a nonliteral pattern, or uses unmodelled ServeMux syntax is
// a generator error rather than a silently omitted route.
func serverRoutes(root string) ([]route, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var files []string
	moduleRoot := ""
	if info.IsDir() {
		moduleRoot = root
		serverDir := filepath.Join(root, "internal", "server")
		entries, err := os.ReadDir(serverDir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
				files = append(files, filepath.Join(serverDir, entry.Name()))
			}
		}
	} else {
		files = []string{root}
	}
	sort.Strings(files)
	fset := token.NewFileSet()
	var install *ast.FuncDecl
	var installFile string
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "installHandler" {
				continue
			}
			if install != nil {
				return nil, fmt.Errorf("%s: ambiguous production installHandler registration closure", fset.Position(fn.Pos()))
			}
			install, installFile = fn, file
		}
	}
	if install == nil || install.Body == nil {
		return nil, fmt.Errorf("production Server.installHandler registration closure not found")
	}
	muxes := map[string]bool{}
	ast.Inspect(install.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, right := range assign.Rhs {
			if i >= len(assign.Lhs) || !isNewServeMux(right) {
				continue
			}
			if left, ok := assign.Lhs[i].(*ast.Ident); ok {
				muxes[left.Name] = true
			}
		}
		return true
	})
	if len(muxes) != 1 {
		return nil, fmt.Errorf("%s: production registration closure must create exactly one http.NewServeMux", fset.Position(install.Pos()))
	}
	var ret []route
	var walkErr error
	ast.Inspect(install.Body, func(node ast.Node) bool {
		if walkErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || !muxes[receiver.Name] {
			return true
		}
		if selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc" {
			walkErr = fmt.Errorf("%s: unsupported production mux method %s", fset.Position(call.Pos()), selector.Sel.Name)
			return false
		}
		if len(call.Args) != 2 {
			walkErr = fmt.Errorf("%s: %s registration has %d arguments, want 2", fset.Position(call.Pos()), selector.Sel.Name, len(call.Args))
			return false
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			walkErr = fmt.Errorf("%s: nonliteral production route registration", fset.Position(call.Args[0].Pos()))
			return false
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			walkErr = fmt.Errorf("%s: invalid route pattern: %w", fset.Position(literal.Pos()), err)
			return false
		}
		r, err := parseServeMuxRoute(pattern)
		if err != nil {
			walkErr = fmt.Errorf("%s: unsupported production route pattern %q: %w", fset.Position(literal.Pos()), pattern, err)
			return false
		}
		r.Handler = handlerIdent(call.Args[1])
		if moduleRoot != "" {
			rel, err := filepath.Rel(moduleRoot, installFile)
			if err != nil {
				walkErr = err
				return false
			}
			r.OriginFile = filepath.ToSlash(rel)
		} else {
			r.OriginFile = filepath.Base(installFile)
		}
		r.OriginLine = fset.Position(call.Pos()).Line
		ret = append(ret, r)
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(ret) == 0 {
		return nil, fmt.Errorf("%s: production registration closure contains no routes", fset.Position(install.Pos()))
	}
	sort.Slice(ret, func(i, j int) bool {
		left := ret[i].Method + " " + ret[i].Path + " " + ret[i].Kind
		right := ret[j].Method + " " + ret[j].Path + " " + ret[j].Kind
		return left < right
	})
	return ret, nil
}

func isNewServeMux(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewServeMux" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "http"
}

func parseServeMuxRoute(pattern string) (route, error) {
	if strings.Contains(pattern, "{$}") || strings.Contains(pattern, "/") && strings.HasPrefix(pattern, "http") {
		return route{}, fmt.Errorf("host and end-anchor patterns are not modelled")
	}
	parts := strings.Fields(pattern)
	var r route
	switch len(parts) {
	case 1:
		r.Path = parts[0]
	case 2:
		r.Method, r.Path = parts[0], parts[1]
	default:
		return route{}, fmt.Errorf("invalid ServeMux pattern")
	}
	if !strings.HasPrefix(r.Path, "/v1/") {
		return route{}, fmt.Errorf("non-v1 route")
	}
	r.Kind = "exact"
	if r.Method == "" && strings.HasSuffix(r.Path, "/") {
		r.Kind = "prefix"
	}
	return r, nil
}

func clientMethods(dir string) ([]clientMethod, error) {
	fs := token.NewFileSet()
	pkgs, err := parser.ParseDir(fs, dir, func(i os.FileInfo) bool {
		return strings.HasSuffix(i.Name(), ".go") && !strings.HasSuffix(i.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	var ret []clientMethod
	for _, f := range pkgs["client"].Files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv != nil && ast.IsExported(fn.Name.Name) {
				call, class, err := classifyClientFunc(fn)
				if err != nil {
					return nil, err
				}
				method := clientMethod{Name: fn.Name.Name, Classification: class}
				if call != nil {
					verb := map[string]string{"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE"}[call.Fun.(*ast.SelectorExpr).Sel.Name]
					path, ok := pathTemplate(call.Args[1])
					if !ok {
						return nil, fmt.Errorf("%s: unsupported path expression", fn.Name.Name)
					}
					method.Method, method.Path, method.Classification = verb, "/v1/"+strings.TrimPrefix(path, "/"), "http"
				}
				ret = append(ret, method)
			}
		}
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Name < ret[j].Name })
	return ret, nil
}

// clientOperations is intentionally type-aware: syntax finds the transport call, while
// go/packages provides the resolved source types used in its body and result.
func clientOperations(m module) ([]operation, []clientMethod, error) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo, Dir: m.Dir}
	loaded, err := packages.Load(cfg, "./internal/client")
	if err != nil {
		return nil, nil, fmt.Errorf("load client package: %w", err)
	}
	if packages.PrintErrors(loaded) > 0 || len(loaded) != 1 {
		return nil, nil, fmt.Errorf("load client package: type errors")
	}
	pkg := loaded[0]
	var ops []operation
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !ast.IsExported(fn.Name.Name) {
				continue
			}
			o := operation{clientMethod: clientMethod{Name: fn.Name.Name, Classification: "non_http"}, StatusSemantics: "source_unspecified", Source: pkg.Fset.Position(fn.Pos()).String()}
			call, class, classifyErr := classifyClientFunc(fn)
			if classifyErr != nil {
				return nil, nil, fmt.Errorf("%s: %w", o.Source, classifyErr)
			}
			if call == nil {
				o.Classification = class
				ops = append(ops, o)
				continue
			}
			verb := map[string]string{"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE"}[call.Fun.(*ast.SelectorExpr).Sel.Name]
			path, ok := pathTemplate(call.Args[1])
			if !ok {
				return nil, nil, fmt.Errorf("%s: unsupported %s path expression", o.Source, o.Name)
			}
			o.Method, o.Path, o.Classification = verb, "/v1/"+strings.TrimPrefix(path, "/"), "http"
			o.PathParams = templateParams(path)
			o.BodyWire, o.HasBody, err = extractBody(fn, call, pkg)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", o.Source, err)
			}
			o.QueryParams, err = extractQueryParams(fn, call)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: unsupported query: %w", o.Source, err)
			}
			o.HeaderParams, err = extractHeaderParams(fn, call)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: unsupported headers: %w", o.Source, err)
			}
			var decodedResult types.Type
			var hasDecodedResult bool
			if o.Name != "SubscribeEvents" {
				decodedResult, hasDecodedResult, err = responseDecodeWire(pkg, fn)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("%s: unsupported %s response decoder: %w", o.Source, o.Name, err)
			}
			sig, ok := pkg.TypesInfo.TypeOf(fn.Name).(*types.Signature)
			if !ok {
				return nil, nil, fmt.Errorf("%s: unsupported %s signature", o.Source, o.Name)
			}
			var results []types.Type
			for i := 0; i < sig.Results().Len(); i++ {
				t := sig.Results().At(i).Type()
				if types.Identical(t, types.Universe.Lookup("error").Type()) {
					continue
				}
				results = append(results, t)
			}
			if o.Name == "SubscribeEvents" {
				// SSE is deliberately represented by EventEnvelope/SSEEvent, not a unary response.
				results = nil
			}
			o.AcceptedStatuses, o.StatusSemantics, err = extractStatusSemantics(pkg, fn)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: unsupported %s response status: %w", o.Source, o.Name, err)
			}
			if hasDecodedResult {
				// The server must serialize the JSON decoder target, not the exported
				// method's convenience projection (for example response.Prompt).
				o.ResultWire, o.HasResult = decodedResult, true
			} else if o.Name != "SubscribeEvents" && len(results) != 0 {
				return nil, nil, fmt.Errorf("%s: %s has no source response decoder", o.Source, o.Name)
			}
			ops = append(ops, o)
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	methods := make([]clientMethod, len(ops))
	for i := range ops {
		methods[i] = ops[i].clientMethod
	}
	return ops, methods, nil
}

// responseStatusSemantics mechanically records the response status guards in
// the exported client method itself. A missing guard is explicit rather than
// silently treated as a 200-only operation.
func responseStatusSemantics(fn *ast.FuncDecl) ([]int, string) {
	statuses := map[int]bool{}
	guarded := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			ident, ok := n.Fun.(*ast.Ident)
			if !ok || ident.Name != "checkStatus" {
				return true
			}
			guarded = true
			if len(n.Args) == 1 {
				statuses[httpStatusOK] = true
			}
			for _, arg := range n.Args[1:] {
				if status, ok := httpStatus(arg); ok {
					statuses[status] = true
				}
			}
		case *ast.BinaryExpr:
			if status, ok := statusComparison(n); ok {
				guarded = true
				statuses[status] = true
			}
		}
		return true
	})
	if !guarded {
		return nil, "source_unspecified"
	}
	values := make([]int, 0, len(statuses))
	for value := range statuses {
		values = append(values, value)
	}
	sort.Ints(values)
	return values, "guarded"
}

const httpStatusOK = 200

func statusComparison(expr *ast.BinaryExpr) (int, bool) {
	if expr.Op != token.EQL && expr.Op != token.NEQ {
		return 0, false
	}
	if isResponseStatusCode(expr.X) {
		return httpStatus(expr.Y)
	}
	if isResponseStatusCode(expr.Y) {
		return httpStatus(expr.X)
	}
	return 0, false
}

func isResponseStatusCode(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "StatusCode"
}

func httpStatus(expr ast.Expr) (int, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	if selector.Sel.Name == "StatusOK" {
		return 200, true
	}
	if selector.Sel.Name == "StatusAccepted" {
		return 202, true
	}
	return 0, false
}

func transportCall(fn *ast.FuncDecl) *ast.CallExpr {
	calls := httpTransportCalls(fn)
	if len(calls) != 1 {
		return nil
	}
	return calls[0]
}

// responseDecodeWire finds the successful response JSON decoder target. The
// target is the public server's response envelope; method return values can be
// projections from that envelope and must never replace its wire shape.
func responseDecodeWire(pkg *packages.Package, fn *ast.FuncDecl) (types.Type, bool, error) {
	var target ast.Expr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Decode" {
			return true
		}
		decoder, ok := selector.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		newDecoder, ok := decoder.Fun.(*ast.SelectorExpr)
		if !ok || newDecoder.Sel.Name != "NewDecoder" {
			return true
		}
		jsonPkg, ok := newDecoder.X.(*ast.Ident)
		if !ok || jsonPkg.Name != "json" {
			return true
		}
		unary, ok := call.Args[0].(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			return true
		}
		// The last decoder is the normal-success decoder: status-specific error
		// branches appear before it in the source.
		target = unary.X
		return true
	})
	if target == nil {
		return nil, false, nil
	}
	t := pkg.TypesInfo.TypeOf(target)
	if t == nil {
		return nil, false, fmt.Errorf("unresolved Decode target")
	}
	return t, true, nil
}

func templateParams(path string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}
	return out
}

func queryParams(fn *ast.FuncDecl, call *ast.CallExpr) []string {
	if len(call.Args) < 3 {
		return nil
	}
	var values *ast.CompositeLit
	if direct, ok := call.Args[2].(*ast.CompositeLit); ok {
		values = direct
	} else {
		id, ok := call.Args[2].(*ast.Ident)
		if !ok || id.Name == "nil" {
			return nil
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, left := range assign.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || name.Name != id.Name || i >= len(assign.Rhs) {
					continue
				}
				if lit, ok := assign.Rhs[i].(*ast.CompositeLit); ok {
					values = lit
				}
			}
			return true
		})
	}
	if values == nil {
		return nil
	}
	var out []string
	for _, elt := range values.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			continue
		}
		name, err := strconv.Unquote(key.Value)
		if err == nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func publicType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if strings.HasPrefix(p.Path(), "github.com/charmbracelet/crush/internal/") {
			return ""
		}
		return p.Name()
	})
}

func pathTemplate(expr ast.Expr) (string, bool) {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		value, err := strconv.Unquote(lit.Value)
		return value, err == nil
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary.Op == token.ADD {
		left, lok := pathTemplate(binary.X)
		right, rok := pathTemplate(binary.Y)
		if lok && rok {
			return left + right, true
		}
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		if sel, ok := expr.(*ast.SelectorExpr); ok {
			if base, ok := sel.X.(*ast.Ident); ok {
				return "{" + base.Name + "_" + sel.Sel.Name + "}", true
			}
			return "{" + sel.Sel.Name + "}", true
		}
		if id, ok := expr.(*ast.Ident); ok {
			return "{" + id.Name + "}", true
		}
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" {
		return "", false
	}
	format, ok := pathTemplate(call.Args[0])
	if !ok {
		return "", false
	}
	for _, arg := range call.Args[1:] {
		name := "value"
		switch v := arg.(type) {
		case *ast.Ident:
			name = v.Name
		case *ast.SelectorExpr:
			if base, ok := v.X.(*ast.Ident); ok {
				name = base.Name + "_" + v.Sel.Name
			} else {
				return "", false
			}
		}
		at := strings.Index(format, "%")
		if at < 0 {
			return "", false
		}
		end := at + 1
		for end < len(format) && strings.ContainsRune("#0- +.0123456789", rune(format[end])) {
			end++
		}
		if end >= len(format) {
			return "", false
		}
		format = format[:at] + "{" + name + "}" + format[end+1:]
	}
	if strings.Contains(format, "%") {
		return "", false
	}
	return format, true
}

func treeDigest(dirs []string) (string, error) {
	h := sha256.New()
	for _, dir := range dirs {
		var files []string
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		sort.Strings(files)
		for _, p := range files {
			b, e := os.ReadFile(p)
			if e != nil {
				return "", e
			}
			fmt.Fprint(h, strings.TrimPrefix(p, dir))
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fail(err error) { fmt.Fprintln(os.Stderr, "protocolgen:", err); os.Exit(1) }
