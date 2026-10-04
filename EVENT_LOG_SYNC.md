# Least-Invasive Path to Event-Log Sync in Crush

## What already exists (surprisingly close)

The codebase is **80% of the way there** without knowing it:

| Approach 3 requirement | Already present | Where |
|---|---|---|
| Append-only event log | SQLite `messages` table, ordered by `created_at ASC` | `db/messages.sql.go` |
| Event broadcasting | `pubsub.Broker[T]` with `created/updated/deleted` events | `internal/pubsub/` |
| Plugin observation of events | `MessageSubscriber` → hooks consume `MessageEvent` channel | `plugin/message.go` |
| Portable snapshot format | `SessionSnapshot` (version + metadata + ordered messages) | `plugin/session_store.go` |
| Export/import over HTTP | `GET /sessions/{id}/export`, `POST /sessions/import` | `acp/server.go` |
| Streaming events during runs | NDJSON with `session.message` + `session.snapshot` events | `acp/server.go` |
| Offset-based reconnection | `handleStreamRunEvents` replays history then tails new events | `acp/server.go` |
| SDK persistence pattern | `Dump → json → Restore → Resume` | `sdks/go/doc.go` |

**The gap**: There's no concept of a **log offset / sync token** that survives
across connections, and no **projection layer** separating the raw event history
from the materialized working memory the LLM sees.

---

## The plan: three surgical additions

### 1. Add a monotonic sequence number to messages

The `messages` table already stores events in order, but clients have no
lightweight cursor. A single migration adds one:

```sql
-- new migration
ALTER TABLE messages ADD COLUMN seq INTEGER;

-- backfill existing
UPDATE messages SET seq = rowid WHERE seq IS NULL;

-- for new rows, use a trigger or default
CREATE TRIGGER messages_set_seq AFTER INSERT ON messages
BEGIN
    UPDATE messages SET seq = NEW.rowid WHERE id = NEW.id AND seq IS NULL;
END;
```

**Why `rowid`**: SQLite `rowid` is already a monotonically increasing integer.
We just surface it as a stable, queryable column. This is the `Last-Event-ID` /
Kafka offset / Matrix sync token — the single concept that makes everything
else work.

**Cost**: One migration, one column, one trigger. Zero changes to existing code
paths.

### 2. Add a `ListMessagesSince` query

One new sqlc query alongside the existing `ListMessagesBySession`:

```sql
-- name: ListMessagesBySessionSince :many
SELECT * FROM messages
WHERE session_id = ? AND seq > ?
ORDER BY seq ASC;
```

Then expose it through `SessionStore`:

```go
// In plugin/session_store.go — add to interface
type SessionStore interface {
    // ... existing methods ...

    // ListMessagesSince returns messages after the given sequence number.
    // Pass seq=0 to get all messages (full hydration).
    ListMessagesSince(ctx context.Context, sessionID string, seq int64) ([]SessionMessage, error)

    // LatestSeq returns the highest sequence number for a session.
    LatestSeq(ctx context.Context, sessionID string) (int64, error)
}
```

And add `Seq int64` to `SessionMessage`:

```go
type SessionMessage struct {
    // ... existing fields ...
    Seq int64 `json:"seq"`
}
```

**Cost**: Two new queries, two new interface methods, one new field. The
existing `ExportSession`/`ImportSession` and all hooks continue working
unchanged.

### 3. Add a delta-sync endpoint to the ACP server

One new HTTP endpoint alongside the existing export/import:

```go
// GET /sessions/{session_id}/sync?since=42
// Returns only messages with seq > 42, plus the session's current metadata.
func (h *ServerHook) handleSyncSession(w http.ResponseWriter, r *http.Request) {
    sessionID := r.PathValue("session_id")
    since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)

    store := h.app.SessionStore()
    session, _ := store.GetSession(r.Context(), sessionID)
    messages, _ := store.ListMessagesSince(r.Context(), sessionID, since)
    latest, _ := store.LatestSeq(r.Context(), sessionID)

    writeJSON(w, http.StatusOK, SyncResponse{
        Session:   session,
        Messages:  messages,
        LatestSeq: latest,
    })
}
```

**Cost**: One new route, one new response type. The existing streaming
(`session.message` events) already carries the data — this just adds a
pull-based complement to the push-based stream.

---

## How the use-cases work after these changes

### Ephemeral agent node

```
1. Agent starts, knows session ID + last seq (from orchestrator / env var)
2. GET /sessions/{id}/sync?since={seq}  →  gets only new messages
3. Projects working memory from the delta (or full log if seq=0)
4. Does work, appends messages via POST /runs
5. Reads final seq from the session.snapshot event
6. Reports seq back to orchestrator, exits
7. Next agent picks up from that seq
```

This is exactly the Kafka consumer group pattern: the orchestrator tracks
offsets, agents are stateless consumers.

### Desktop → mobile handoff

```
Desktop:
1. User works normally. Local SQLite has all messages with seq numbers.
2. Closes laptop. Last seen seq = 847.

Mobile:
3. Opens app, connects to same ACP server (or syncs via shared storage).
4. GET /sessions/{id}/sync?since=847  →  gets 0 messages (up to date)
5. User continues working. New messages get seq 848, 849, ...
6. Closes mobile. Last seen seq = 852.

Desktop:
7. Opens laptop, GET /sessions/{id}/sync?since=847  →  gets messages 848-852
8. Resumes seamlessly.
```

If both are online simultaneously, the existing `session.message` streaming
events provide real-time sync — each client just tracks its own `seq` watermark
independently (the Matrix device model).

---

## What this does NOT change

- The SQLite database remains the source of truth
- The `pubsub.Broker` event fan-out is unchanged
- All existing hooks (OTLP, agent-status, tempotown) work identically
- `ExportSession`/`ImportSession` continue working for full snapshots
- The SDK `Dump`/`Restore`/`Resume` pattern is unchanged
- No new dependencies, no new services to run

---

## Future projection layer (optional, not needed now)

The "projection function" from Approach 3 would be a follow-up: a function that
takes `[]SessionMessage` and produces the LLM's context window (applying
summarization, truncation, memory extraction). Today, crush already does this in
its prompt builder — it just isn't formalized as a versioned projection. When you
want mobile to show a summary view while desktop shows full context, you'd add a
`ProjectionFunc` type and let clients specify which projection to apply at sync
time. But that's a separate concern from the transport layer described above.

---

## Summary

| Change | Files touched | Risk |
|--------|--------------|------|
| `seq` column + trigger | 1 migration file | None (additive) |
| `ListMessagesSince` + `LatestSeq` | `messages.sql`, `messages.sql.go`, `session_store.go`, adapter | Low (new queries only) |
| `Seq` field on `SessionMessage` | `session_store.go` | Low (additive JSON field) |
| `/sync` endpoint | `acp/server.go` | Low (new route, no changes to existing) |

Four files, zero breaking changes, and crush speaks the event-log-with-offsets
protocol.
