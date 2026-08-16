# Crush Modules client/server protocol

`github.com/aleksclark/crush-modules/clientserver` is a **public, versioned release artifact** for the Crush HTTP/SSE client-server boundary. It is owned by Crush Modules, not Charm, and does not imply upstream Charm support.

Consumers import the module directly:

```go
import protocol "github.com/aleksclark/crush-modules/clientserver"

var _ protocol.Server = (*myServer)(nil)
```

`protocol.Server` plus `protocol.Register` provide an adapter boundary for each generated stock route. `Routes`, `ClientMethods`, `EventEnvelope`, request/response DTOs, and bearer metadata are generated from the exact resolved `github.com/charmbracelet/crush` source. The adapter receives Authorization metadata but never a configured token value.

## Source and reproducibility

The authoritative input is the resolved dependency reported by:

```sh
go list -m -json github.com/charmbracelet/crush
```

The generator reads only that module's `internal/client`, `internal/proto`, and `internal/server` source, records the resolved module version, source Git commit, and a deterministic source-tree SHA-256 in `protocol_gen.go`. It never uses a hard-coded sibling path and generated output never imports a Crush `internal/*` package.

Generated files are checked in so a checked-out/published module is immediately importable. They begin with `DO NOT EDIT`; change source/config/generator, then regenerate:

```sh
go generate ./...
go -C tools/protocolgen run . -out ../.. -check
```

`clientserver:check` regenerates to a temporary directory and byte-compares it with the tracked artifact. Tests also prove repeatability, reject a deliberately mutated artifact, JSON round-trip representative source-wire JSON, and compile an unrelated temporary Go module that implements `protocol.Server`.

The generator fails closed if the resolved source/module metadata or source syntax is unavailable. Route registrations and exported client method inventory are mechanically collected; the generated `Routes` registry includes all stock registrations (including non-client provider/docs discovery surfaces), avoiding silent route omission.
