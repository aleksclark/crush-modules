# Session Portability, Multiplayer, and Multiviewer Patterns

A survey of established patterns across computing history for making sessions
portable, shareable, and observable by multiple participants.

---

## Table of Contents

1. [Terminal Multiplexers (1987–present)](#1-terminal-multiplexers)
2. [X11 Window System (1984–present)](#2-x11-window-system)
3. [VNC and the RFB Protocol (1998–present)](#3-vnc-and-the-rfb-protocol)
4. [RDP and Session Shadowing (1996–present)](#4-rdp-and-session-shadowing)
5. [Mosh — State Synchronization Protocol (2012–present)](#5-mosh--state-synchronization-protocol)
6. [Plan 9 and the 9P Protocol (1992–present)](#6-plan-9-and-the-9p-protocol)
7. [Erlang/OTP — Hot Code Swapping (1986–present)](#7-erlangotp--hot-code-swapping)
8. [The Actor Model (1973–present)](#8-the-actor-model)
9. [IRC, XMPP, and Matrix (1988–present)](#9-irc-xmpp-and-matrix)
10. [HTTP Session Management (1994–present)](#10-http-session-management)
11. [Operational Transform and CRDTs (2006–present)](#11-operational-transform-and-crdts)
12. [Consensus Protocols — Raft and Paxos](#12-consensus-protocols--raft-and-paxos)
13. [Kafka Consumer Offsets and Redis Cluster Slots](#13-kafka-consumer-offsets-and-redis-cluster-slots)
14. [Cross-Cutting Themes](#14-cross-cutting-themes)

---

## 1. Terminal Multiplexers

**GNU Screen (1987) · tmux (2007)**

### Core Pattern: Persistent Server Process + Socket-Based Client Attachment

Both GNU Screen and tmux decouple long-running processes from the terminal
connection that started them. A server process owns all session state; clients
connect and disconnect via Unix domain sockets without affecting the running
processes.

### Architecture

```
┌──────────┐     Unix Socket     ┌──────────────────────────┐
│ Client A ├────────────────────►│                          │
└──────────┘                     │    Server Process        │
                                 │  ┌────────┐ ┌────────┐  │
┌──────────┐     Unix Socket     │  │ Pane 1 │ │ Pane 2 │  │
│ Client B ├────────────────────►│  │ (bash) │ │ (vim)  │  │
└──────────┘                     │  └────────┘ └────────┘  │
                                 └──────────────────────────┘
```

### Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Server owns all state | Sessions survive client disconnects, crashes, network drops |
| Unix domain sockets for IPC | Low-latency, file-permission-based access control |
| Terminal emulation in server | Clients can be heterogeneous terminal types |
| Named sessions | Multiple independent sessions on one machine |

### Multi-Attach Patterns

- **Screen `screen -x`**: All clients see the same window, fully synchronized.
  All-or-nothing sharing.
- **tmux multi-client**: Clients attach to the same session but can view
  different windows independently. When two clients view the same window, they
  are synchronized. This was a core design goal — tmux was created specifically
  because Screen could not do this.

### Portability Properties

- Session state is fully server-side (scrollback, paste buffers, working dirs).
- Detach/reattach is instantaneous — no state transfer needed.
- Sessions are named and discoverable via filesystem (socket files).
- No protocol-level authentication beyond Unix file permissions.

### Adoption

Screen has been the standard for remote server administration since the early
1990s. tmux is the default on OpenBSD since 2009 and is near-universal on
Linux/macOS developer machines today. Combined, they represent ~35 years of
production use.

---

## 2. X11 Window System

**MIT Project Athena (1984) · X11 release (1987)**

### Core Pattern: Network-Transparent Display Protocol with Reversed Client-Server

X11's defining innovation is that the **server runs where the display is** and
**clients run where the computation is**. Applications (clients) on any machine
send drawing commands and receive input events over the network.

### Architecture

```
Remote Machine                  Local Machine
┌────────────┐    X Protocol    ┌─────────────┐
│ Application├─────────────────►│  X Server   │
│ (X Client) │  TCP / Unix      │  (display,  │
└────────────┘  domain socket   │   keyboard, │
                                │   mouse)    │
                                └─────────────┘
```

### Protocol Design

The X protocol is full-duplex with four message types:

| Type | Direction | Purpose |
|------|-----------|---------|
| Request | Client → Server | Drawing commands, resource queries |
| Reply | Server → Client | Responses to queries |
| Event | Server → Client | Input events, window state changes |
| Error | Server → Client | Error notifications |

### Key Design Decisions

- **Resource identifiers (XIDs)** are client-allocated from server-provided
  ranges, reducing round-trips.
- **Extension mechanism** allows new functionality without protocol changes.
  Clients query supported extensions at connect time.
- **Display addressing** via `hostname:display.screen` makes routing explicit.
- **No rendering model** — X is a wire protocol, not a rendering engine. Window
  managers and compositors are separate clients.

### X11 Forwarding via SSH

SSH tunnels X11 connections through encrypted channels:

1. SSH server opens a proxy X listener at `localhost:6010+`
2. Remote `$DISPLAY` is set to the proxy address
3. SSH generates a fake auth cookie for the remote side
4. On connection, SSH validates the fake cookie, substitutes the real one, and
   forwards to the local X server

**Trusted (`-Y`)** gives full access. **Untrusted (`-X`)** restricts screen
capture, input injection, and clipboard access.

### Session Portability

X11 sessions are **not portable** — they are bound to the display connection.
If the connection drops, the application typically crashes. This is the inverse
of tmux's model: X11 makes the *display* portable (any machine can be the
server), but the *session* is ephemeral.

### Wayland Contrast

Wayland (2008) eliminated network transparency in favor of zero-copy local
rendering and strong client isolation. This is a deliberate trade: X11's network
model is a security liability (any connected client can read the screen), while
Wayland provides per-application sandboxing at the cost of needing separate
tools (VNC, RDP) for remote access.

### Adoption

X11 has been the standard Unix display system for nearly 40 years. It remains
dominant for remote GUI use cases (SSH forwarding). Wayland is now the default
on GNOME and KDE but X11 persists where network transparency is required.

---

## 3. VNC and the RFB Protocol

**Olivetti & Oracle Research Lab, Cambridge (1998) · RFC 6143 (2011)**

### Core Pattern: Framebuffer Pixel Sharing with Demand-Driven Updates

VNC transmits screen content as pixel rectangles. The server maintains a
framebuffer; clients request updates and receive changed regions. Multiple
clients can connect simultaneously — this is a first-class design feature.

### Architecture

```
┌──────────┐
│ Viewer A ├──┐
└──────────┘  │     RFB Protocol      ┌────────────┐
              ├───────────────────────►│ VNC Server │
┌──────────┐  │   (pixel rectangles,  │ (owns the  │
│ Viewer B ├──┘    input events)       │ framebuf)  │
└──────────┘                           └────────────┘
```

### Protocol Design (RFB)

**Handshake**: Version negotiation → security type → authentication → server
init (framebuffer dimensions, pixel format).

**Steady state**: Client sends `FramebufferUpdateRequest`. Server responds with
rectangles of changed pixels using a negotiated encoding.

| Encoding | Strategy | Use Case |
|----------|----------|----------|
| Raw | Uncompressed pixels | Universal fallback |
| CopyRect | Pointer to existing region | Window moves, scrolling |
| Hextile | 16×16 tile compression | General use |
| ZRLE | Zlib + run-length on tiles | Best compression ratio |
| Tight | JPEG for photographic regions | Images, video |

**Input**: KeyEvent, PointerEvent, ClientCutText (clipboard).

### Multiviewer Properties

- All connected viewers see the same framebuffer.
- Multiple viewers can send input simultaneously (no arbitration in the protocol).
- Viewers are stateless — disconnect and reconnect shows current state.
- The protocol is **demand-driven**: servers only send updates when clients ask,
  making it adaptive to varying client bandwidth.

### Session Portability

VNC sessions are inherently portable: the framebuffer exists on the server
regardless of viewer connections. A viewer can disconnect, move to a different
network, and reconnect to see the current state. There is no sync or replay —
just the current frame.

### Adoption

VNC is deployed across platforms for remote administration, technical support,
and headless system management. Major implementations include RealVNC,
TigerVNC, TightVNC, and UltraVNC. The RFB protocol is an IETF standard
(RFC 6143).

---

## 4. RDP and Session Shadowing

**Microsoft (1996) · Based on ITU-T T.128**

### Core Pattern: Virtual Session Creation with Drawing-Command Transmission

Unlike VNC (which shares one physical framebuffer), RDP creates **independent
virtual desktop sessions** per user. Each connection gets its own isolated
environment. This is fundamentally a different model.

### Architecture

```
┌──────────┐   RDP    ┌──────────────────────────┐
│ Client A ├─────────►│ Session 1 (User A)       │
└──────────┘          │ ┌──────────────────────┐  │
                      │ │ Independent desktop  │  │
┌──────────┐   RDP    │ └──────────────────────┘  │
│ Client B ├─────────►│ Session 2 (User B)       │  RDP Server
└──────────┘          │ ┌──────────────────────┐  │  (Windows)
                      │ │ Independent desktop  │  │
                      │ └──────────────────────┘  │
                      └──────────────────────────┘
```

### Key Differences from VNC

| Aspect | VNC | RDP |
|--------|-----|-----|
| Session model | Shared framebuffer | Independent virtual sessions |
| Data transmitted | Pixels | Drawing commands (GDI) |
| Bandwidth | Higher (pixel data) | Lower (vector commands) |
| Multi-user | All see same screen | Each gets own desktop |
| Platform | Cross-platform | Windows-centric |

### Session Shadowing (Multiviewer)

RDP does support a VNC-like multiviewer mode called **session shadowing**:

- An administrator can view and optionally control another user's active session.
- Configured via registry: `HKLM\...\Terminal Services\Shadow` (values 0–4
  control view-only vs. control, consent vs. silent).
- Initiated with `mstsc.exe /shadow:<sessionID> [/control] [/noConsentPrompt]`.
- The shadowed user and administrator see the same session content.
- Only one shadow viewer per session.

### Session Portability

RDP sessions on Windows Server persist when disconnected. A user can disconnect,
move to another machine, and reconnect to find their session exactly as left
(running applications, open files, etc.). This is server-managed state with
client reconnection, similar to tmux but at the desktop level.

### Adoption

RDP is the standard for Windows enterprise remote access, thin client
deployments, and cloud desktop services (Azure Virtual Desktop, AWS WorkSpaces).
~30 years of production use.

---

## 5. Mosh — State Synchronization Protocol

**MIT (2012)**

### Core Pattern: UDP-Based Terminal State Sync with Predictive Local Echo

Mosh replaces SSH's byte-stream-over-TCP model with a state synchronization
approach over UDP. Both ends maintain a copy of the terminal state; only
differences are transmitted.

### Architecture

```
┌────────────┐   SSH (bootstrap)   ┌──────────────┐
│ mosh-client├────────────────────►│  mosh-server │
│            │                     │              │
│ Local copy │◄═══ UDP datagrams ══►│ Server copy  │
│ of terminal│   (state deltas,    │ of terminal  │
│ state      │    AES-128-OCB)     │ state        │
└────────────┘                     └──────────────┘
```

### State Synchronization Protocol (SSP)

Instead of transmitting every byte of terminal output, SSP sends **state
transitions** — diffs between terminal frames.

| Property | SSH | Mosh/SSP |
|----------|-----|----------|
| Transport | TCP (ordered, reliable) | UDP (unordered, lossy) |
| Data model | Byte stream | Terminal state objects |
| Packet loss | Waits for retransmit | Discards stale updates |
| IP roaming | Session dies | Transparent (auth by shared key) |
| Latency compensation | None | Predictive local echo |

### Key Design Decisions

- **UDP datagrams are independently encrypted** (AES-128-OCB with sequence
  number nonces). No packet depends on any other — old updates can be safely
  dropped.
- **Predictive local echo**: The client predicts how keystrokes affect the
  screen and displays predictions immediately (underlined). Predictions are
  corrected when the server's authoritative state arrives.
- **SSH is only for bootstrap**: Authentication and key exchange use SSH.
  After that, the SSH connection is closed and the session runs entirely
  over UDP.
- **IP roaming**: Since UDP is connectionless, the server simply accepts
  authenticated packets from whatever IP they arrive from. Network changes
  (WiFi → cellular) are transparent.

### Session Portability

Mosh sessions survive network changes, laptop sleep/wake cycles, and transient
connectivity loss. The session persists on the server; the client can
reconnect from any network. However, unlike tmux, Mosh sessions are bound to
the mosh-server process — there is no detach/reattach to a different client
terminal.

**Common pairing**: Mosh + tmux gives both network resilience (Mosh) and
terminal multiplexing/detach (tmux).

---

## 6. Plan 9 and the 9P Protocol

**Bell Labs (1992) · Rob Pike, Ken Thompson, et al.**

### Core Pattern: Everything-is-a-File-Server with Per-Process Namespaces

Plan 9 extends Unix's "everything is a file" philosophy to the network. All
resources — files, devices, network connections, processes — are accessed through
**9P**, a single file-serving protocol. Each process has its own **namespace**
(mount table), making resource composition per-process and per-user.

### Architecture

```
Process Namespace (customizable per-process):
/
├── bin/        ← union of local + remote binaries
├── dev/        ← local or remote devices via 9P
├── net/        ← mounted from network server via 9P
├── mnt/
│   └── wiki/   ← wikifs (a 9P file server)
└── proc/       ← process info, also a 9P server
```

### 9P Protocol

9P is a request-response RPC protocol with these core operations:

| Operation | Purpose |
|-----------|---------|
| `attach` | Establish session with file tree |
| `walk` | Traverse path components |
| `open` / `create` | Open or create files |
| `read` / `write` | Data transfer |
| `stat` / `wstat` | Metadata |
| `clunk` | Release file handle |

### Key Design Decisions

- **Per-process namespaces**: Each process can `mount` and `bind` different 9P
  servers at different paths. This makes resource composition a per-process
  operation, not a system-wide one.
- **No caching in the protocol**: The server's memory is the cache. This
  simplifies consistency — clients always see the current state.
- **Union directories**: Multiple file trees can be overlaid at a single path.
  `bin/` might union architecture-specific, user-specific, and system binaries.
- **Applications are 9P servers**: The text editor (acme), window system (rio),
  and many utilities expose their state as synthetic file trees navigable
  by other programs.

### Session Portability via `cpu`

The `cpu` command exemplifies Plan 9's portability model:

1. User runs `cpu` on a local terminal.
2. The local terminal's devices (mouse, keyboard, display) are **exported** to
   the remote machine via 9P.
3. A shell runs on the remote CPU but performs I/O on the local terminal.
4. The remote process's namespace is assembled from both local and remote
   resources.

This is not remote desktop (sending pixels) or SSH (sending bytes). It is
**namespace composition** — the session is defined by which 9P servers are
mounted where, and those mounts can span machines transparently.

### Influence

9P lives on in Linux's `v9fs`, QEMU's virtio-9p, and WSL2's Plan 9 file
sharing. The per-process namespace idea influenced Linux mount namespaces
(containers). The "everything is a file server" pattern influenced FUSE.

---

## 7. Erlang/OTP — Hot Code Swapping

**Ericsson (1986)**

### Core Pattern: In-Place State Transformation Without Session Interruption

Erlang was designed for telecom switches that must run for years without
downtime. Hot code swapping upgrades running code while preserving process state,
without dropping connections.

### Mechanism

```
1. Suspend process       (:sys.suspend)
2. Load new module        (:code.load_file)
3. Transform state        (code_change/3 callback)
4. Resume process         (:sys.resume)
```

The `code_change/3` callback receives the old state and returns the new state
shape. Two versions of a module can coexist — the "current" and the "old" — so
processes can finish in-flight work before upgrading.

### Key Design Decisions

- **Module-level granularity**: Individual modules are hot-swapped, not the
  entire system.
- **Explicit state migration**: The `code_change/3` callback is a mandatory
  contract for stateful processes. State transformation is not implicit.
- **Release management**: `.appup` and `.relup` files describe upgrade paths
  between specific versions, including which modules to load and in what order.
- **Process isolation**: Each Erlang process has its own heap. Upgrading one
  process cannot corrupt another.

### Session Portability

Erlang's model is **session continuity** rather than session portability. The
session (process) stays on the same node but its code and state shape evolve
underneath it. Combined with Erlang's distributed process model (processes on
different nodes communicate transparently via message passing), this enables
rolling upgrades across a cluster without dropping any sessions.

### Adoption

Used in production at Ericsson for decades. WhatsApp, RabbitMQ, CouchDB, and
Discord use Erlang/OTP. The hot code swapping pattern is unique to the BEAM VM
ecosystem.

---

## 8. The Actor Model

**Hewitt, Bishop, Steiger (1973)**

### Core Pattern: Isolated Actors with Address-Passing Messages

The actor model defines concurrent computation in terms of **actors** — entities
that can only interact through asynchronous message passing. Each actor:

1. Processes one message at a time
2. Can send messages to actors it knows
3. Can create new actors
4. Can change its own behavior for the next message

### Session Portability via Isolation

Because actors share nothing (no shared memory, no global state), they are
inherently portable:

- **Location transparency**: An actor's address does not encode its physical
  location. Messages can be routed across machines without the sender knowing.
- **Serializable mailbox**: An actor's pending messages and behavior function
  are self-contained, making migration theoretically possible.
- **Supervision trees** (Erlang/Akka): If an actor crashes, its supervisor can
  restart it on the same or different node with a known initial state.

### Implementations

| System | Migration Model |
|--------|----------------|
| Erlang/OTP | Processes stay put; code migrates to them |
| Akka (JVM) | Cluster sharding redistributes actors; state via event sourcing |
| Orleans (.NET) | Virtual actors activated on any silo; state persisted externally |
| Ray (Python) | Tasks placed on any worker; state in object store |

### Key Pattern

The actor model establishes that **if state is encapsulated and communication is
via messages, then the unit of computation becomes portable**. This is the
theoretical foundation for microservices, serverless functions, and distributed
agent systems.

---

## 9. IRC, XMPP, and Matrix

### Evolution of Multi-Device Session Portability in Messaging

These three protocols span 36 years and demonstrate the progressive addition of
session portability to real-time communication.

### IRC (1988) — No Session Portability

- Stateless: messages are delivered to connected clients only.
- No message history, no offline delivery, no multi-device support.
- One nickname = one connection. Nickname conflicts are resolved by force.
- **Workaround**: Bouncers (ZNC, BNC) act as persistent proxy clients,
  maintaining the connection and buffering messages. This is the tmux pattern
  applied to IRC — a persistent server process that clients attach to.

### XMPP/Jabber (1999) — Resource-Based Multi-Device

XMPP introduced several session portability primitives via extensions (XEPs):

| Extension | Function |
|-----------|----------|
| Resource binding (RFC 6120) | Each device gets a unique JID: `user@domain/device` |
| Stream Management (XEP-0198) | Resume interrupted connections without losing stanzas |
| Message Archive Management (XEP-0313) | Server-side message history |
| Message Carbons (XEP-0280) | Live cross-device message sync |
| Offline Storage (XEP-0160) | Server stores messages for offline users |

**Key pattern**: The **resource** concept allows multiple simultaneous sessions
under one identity. Stream Management provides a **resume token** with a window
(typically 5–10 minutes) during which the server holds unacknowledged stanzas.

### Matrix (2014) — First-Class Session Portability

Matrix was designed from day one with session portability as a core property:

- **Sync tokens**: Each client tracks its position in the event stream via an
  opaque token. Reconnecting with a token returns only events since that point.
- **Immutable event log**: All messages are stored in an append-only log on
  the homeserver. History is never lost due to client disconnection.
- **Device model**: Each client registers as a device with its own keys and
  sync position. Multiple devices operate independently.
- **Federation**: Homeservers replicate event logs to each other, so messages
  are available even if the originating server is temporarily down.

### Pattern Evolution

```
IRC (1988)     → Ephemeral sessions, no portability
  ↓
XMPP (1999)    → Resource binding + server-side archives (bolt-on)
  ↓
Matrix (2014)  → Immutable log + sync tokens + device model (native)
```

The trend is clear: session portability moved from being a non-goal (IRC) to
a bolt-on extension (XMPP) to a foundational design property (Matrix).

---

## 10. HTTP Session Management

### Cookies and Tokens (1994–present)

HTTP is stateless by design. Session management is layered on top.

### Pattern Taxonomy

| Pattern | State Location | Portability | Revocation |
|---------|---------------|-------------|------------|
| Session cookies | Server (memory/DB/Redis) | Tied to cookie jar | Immediate (server deletes) |
| JWT access tokens | Client (self-contained) | Portable (any server can verify) | Hard (wait for expiry) |
| OAuth 2.0 refresh tokens | Server | Delegated (cross-service) | Immediate (server revokes) |

### Key Patterns

**Server-side sessions (cookies)**: A session ID in a cookie maps to server-side
state. Horizontal scaling requires a shared session store (Redis, Memcached).
This is the tmux model applied to HTTP — the server owns the state, the client
holds a handle.

**Stateless tokens (JWT)**: The token itself contains the session state
(claims), signed cryptographically. Any server can verify without shared state.
Trade-off: revocation requires either short expiry or a deny-list.

**Hybrid (access + refresh)**: Short-lived JWT for API calls (15 min), long-lived
refresh token stored server-side for renewal. Combines portability of JWTs with
revocability of server-side sessions.

### WebSocket Session Persistence

WebSockets maintain a persistent bidirectional connection, creating new session
management challenges:

- **Sticky sessions**: Load balancer routes client to same server. Simple but
  limits scaling.
- **Shared state store**: Session state in Redis; any server can handle
  reconnection. More complex but horizontally scalable.
- **Connection resumption tokens**: Client stores a token with a version number;
  on reconnect, server replays missed events from that version forward. This is
  the Matrix sync-token pattern.

### Server-Sent Events

SSE provides unidirectional server-to-client push with built-in reconnection:
- `Last-Event-ID` header on reconnect tells the server where to resume.
- `retry` field controls reconnection interval.
- This is a simple, HTTP-native version of the sync-token pattern.

---

## 11. Operational Transform and CRDTs

### Multi-Writer Session Sharing for Collaborative Editing

These two approaches solve the fundamental problem of multiple users editing
the same document simultaneously.

### Operational Transform (OT)

**Google Docs (2006–present)**

OT transforms concurrent operations so they can be applied in any order and
converge to the same state.

```
User A: Insert "x" at position 1
User B: Delete at position 4
           ↓
Server transforms both operations,
adjusting indices to account for
the other operation.
           ↓
Both clients converge to same result.
```

| Property | Value |
|----------|-------|
| Coordination | Central server required |
| Consistency | Strong (server is authority) |
| Complexity | High (transform functions are mathematically subtle) |
| Latency | Low (server applies and broadcasts immediately) |
| Undo/redo | Difficult to implement correctly |

### CRDTs (Conflict-free Replicated Data Types)

CRDTs embed enough metadata in the data structure itself that all replicas
automatically converge, without a central server.

```
User A (offline): Insert "Hello"
User B (offline): Insert "World"
       ↓
Both come online, exchange state
       ↓
CRDT merge: deterministic interleaving
based on unique character IDs.
```

| Property | Value |
|----------|-------|
| Coordination | None required (peer-to-peer capable) |
| Consistency | Strong eventual consistency |
| Complexity | Moderate (data structure design) |
| Metadata overhead | High (unique IDs, tombstones) |
| Offline support | Native |

### Key Libraries

| Library | Model | Used By |
|---------|-------|---------|
| ShareDB | OT | CodeSandbox, various editors |
| Yjs | CRDT | Many collaborative editors |
| Automerge | CRDT | Structured data collaboration |

### Pattern Significance

OT and CRDTs represent two poles of a trade-off fundamental to session sharing:

- **OT**: Central authority + low overhead, but server dependency.
- **CRDTs**: No authority needed + offline-first, but higher metadata cost.

Most production systems use a hybrid: CRDTs for the data model with a central
server for ordering and presence awareness.

---

## 12. Consensus Protocols — Raft and Paxos

### State Machine Replication Across Nodes

Consensus protocols ensure multiple nodes agree on the same sequence of state
transitions, enabling any node to serve as the "session owner" after a failure.

### Raft (2014)

Raft decomposes consensus into three subproblems:

1. **Leader election**: Randomized timeouts trigger elections; first candidate
   to win majority becomes leader.
2. **Log replication**: Leader appends entries and replicates to followers via
   `AppendEntries` RPC.
3. **Safety**: A candidate must have a log at least as up-to-date as any voter
   to be elected, preventing committed entry loss.

### Paxos (1989)

Paxos separates roles (proposer, acceptor, learner) and achieves consensus
through two phases (Prepare, Accept). More general but harder to implement.

### Session Portability via Consensus

| Aspect | Pattern |
|--------|---------|
| Leader failure | New leader elected; committed state is preserved |
| State transfer | Via replicated log — new leader has all committed entries |
| Client redirect | Clients detect leader change and reconnect |
| Split-brain prevention | Term numbers (Raft) / proposal numbers (Paxos) |

### Adoption

| Protocol | Used By |
|----------|---------|
| Raft | etcd (Kubernetes), CockroachDB, TiKV, Consul, HashiCorp Vault |
| Paxos/ZAB | ZooKeeper, Google Chubby, Google Spanner |

These protocols are the foundation for any system that needs to maintain a
"session" (state machine) that survives node failures.

---

## 13. Kafka Consumer Offsets and Redis Cluster Slots

### Application-Level Session Portability

### Kafka Consumer Groups

Kafka tracks each consumer's position in the message log as an **offset** —
a number stored in the `__consumer_offsets` internal topic.

- **Session identity**: (group_id, topic, partition) tuple.
- **Portability**: When a consumer crashes, another consumer in the group picks
  up the partition and resumes from the last committed offset.
- **Cooperative rebalancing** (Kafka 2.4+): Only affected partitions are
  reassigned during rebalancing; unaffected consumers continue processing.
- **Static membership**: A consumer can rejoin with the same `group.instance.id`
  without triggering rebalance, as long as it returns within `session.timeout.ms`.

### Redis Cluster Hash Slots

Redis Cluster divides the keyspace into 16,384 hash slots. Each master node
owns a subset.

- **Session identity**: Hash slot ownership.
- **Portability**: Slots can migrate between nodes live. During migration,
  clients receive `-MOVED` or `-ASK` redirections to find the new owner.
- **Discovery**: Clients learn topology changes through error responses — no
  separate discovery protocol needed.

### Common Pattern

Both systems implement **explicit position tracking with server-side storage**:
the session's position (offset or slot) is a first-class, persistent data item
that can be transferred to a new owner when the current owner fails.

---

## 14. Cross-Cutting Themes

### Theme 1: Server Owns State, Client Holds Handle

The most widely adopted pattern. The server maintains all session state; the
client holds a lightweight reference (socket path, session ID, cookie, sync
token). Disconnection and reconnection are cheap because no state transfer is
needed.

**Examples**: tmux, VNC, RDP, HTTP sessions, Matrix sync tokens.

### Theme 2: State Synchronization Over State Transfer

Instead of transferring complete state on every reconnection, send deltas from
a known checkpoint.

**Examples**: Mosh/SSP (terminal state diffs), Matrix (sync tokens), Kafka
(consumer offsets), SSE (`Last-Event-ID`).

### Theme 3: Named, Discoverable Sessions

Sessions with stable identifiers that survive client disconnection and can be
found by new clients.

**Examples**: tmux named sessions via socket files, RDP session IDs, XMPP
resource JIDs, Kafka consumer group IDs.

### Theme 4: Namespace Composition Over Session Migration

Instead of moving sessions between machines, compose a session from resources
on multiple machines. The session's identity is its namespace, not its location.

**Examples**: Plan 9 per-process namespaces, X11 display forwarding, Docker
bind mounts and overlay filesystems.

### Theme 5: Isolation Enables Portability

The more isolated a unit of computation is (no shared memory, no global state,
serializable messages), the more portable it becomes.

**Examples**: Actor model, Erlang processes, containers, serverless functions,
CRDTs.

### Theme 6: Multi-Viewer as First-Class vs. Bolt-On

Systems designed for multiviewer from day one (VNC, tmux, Matrix, CRDTs) have
cleaner models than systems that added it later (IRC bouncers, RDP shadowing,
XMPP Carbons).

### Summary Table

| System | Year | Session Portability | Multi-Viewer | State Location |
|--------|------|-------------------|--------------|----------------|
| Actor Model | 1973 | Theoretical (address passing) | N/A | Per-actor |
| X11 | 1984 | Display portable, session ephemeral | Extension (shared display) | X server |
| Erlang/OTP | 1986 | In-place upgrade (hot swap) | N/A | Per-process |
| GNU Screen | 1987 | Detach/reattach via socket | `screen -x` (synchronized) | Server process |
| IRC | 1988 | None (bouncer workaround) | Channels (broadcast) | None (ephemeral) |
| Paxos | 1989 | Leader failover | N/A (infra) | Replicated log |
| Plan 9 / 9P | 1992 | Namespace composition | Shared file servers | Per-process namespace |
| HTTP Cookies | 1994 | Server-side session store | N/A | Server |
| RDP | 1996 | Disconnect/reconnect virtual session | Session shadowing | Server process |
| VNC / RFB | 1998 | Reconnect to current frame | Native multi-viewer | Server framebuffer |
| XMPP | 1999 | Resource binding + stream resume | Message Carbons | Server archive |
| Google Docs OT | 2006 | Server-authoritative log | Native multi-editor | Central server |
| tmux | 2007 | Detach/reattach, per-window viewing | Multi-client, independent windows | Server process |
| Mosh / SSP | 2012 | IP roaming via UDP state sync | Single client | Client + server copies |
| Raft | 2014 | Leader election + log replay | N/A (infra) | Replicated log |
| Matrix | 2014 | Sync tokens + device model | Native (room model) | Homeserver event log |
| CRDTs | ~2011+ | Merge-on-reconnect | Native multi-writer | Every replica |
