#!/usr/bin/env bash
#
# ACP Client Integration Test
#
# Starts a Crush instance in headless serve mode with a mock LLM backend,
# then uses acp-client to have a 3-message conversation over ACP.
#
# Requirements:
#   - dist/crush built with acp-server plugin (task distro:all)
#   - dist/acp-client built (go build -o dist/acp-client ./cmd/acp-client)
#   - python3 available (for mock LLM)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
CRUSH_BINARY="$PROJECT_DIR/dist/crush"
ACP_CLIENT="$PROJECT_DIR/dist/acp-client"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${CYAN}[test]${NC} $*" >&2; }
pass() { echo -e "${GREEN}[PASS]${NC} $*" >&2; }
fail() { echo -e "${RED}[FAIL]${NC} $*" >&2; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*" >&2; }

PIDS=()
TMPDIR_ROOT=""
cleanup() {
    log "Cleaning up..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 0.5
    for pid in "${PIDS[@]}"; do
        kill -9 "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    done
    if [[ -n "$TMPDIR_ROOT" ]]; then
        rm -rf "$TMPDIR_ROOT"
    fi
    log "Done."
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Preflight checks
# ---------------------------------------------------------------------------
if [[ ! -x "$CRUSH_BINARY" ]]; then
    fail "Crush binary not found at $CRUSH_BINARY — run 'task distro:all' first"
    exit 1
fi
if ! "$CRUSH_BINARY" --list-plugins 2>/dev/null | grep -q "acp-server"; then
    fail "acp-server hook not registered in crush binary"
    exit 1
fi
if [[ ! -x "$ACP_CLIENT" ]]; then
    fail "acp-client binary not found at $ACP_CLIENT — run: go build -o dist/acp-client ./cmd/acp-client"
    exit 1
fi
if ! command -v python3 &>/dev/null; then
    fail "python3 is required for mock LLM"
    exit 1
fi
pass "Binaries and dependencies found"

# ---------------------------------------------------------------------------
# Pick random high ports
# ---------------------------------------------------------------------------
random_port() {
    local port
    while true; do
        port=$((RANDOM % 10000 + 50000))
        if ! ss -tln 2>/dev/null | grep -q ":$port " && \
           ! netstat -tln 2>/dev/null | grep -q ":$port "; then
            echo "$port"
            return
        fi
    done
}

ACP_PORT=$(random_port)
LLM_PORT=$(random_port)
log "Ports: ACP=$ACP_PORT  LLM=$LLM_PORT"

# ---------------------------------------------------------------------------
# Temp directory
# ---------------------------------------------------------------------------
TMPDIR_ROOT=$(mktemp -d)
WORK_DIR="$TMPDIR_ROOT/work"
mkdir -p "$WORK_DIR/.crush"
touch "$WORK_DIR/.crush/init"
mkdir -p "$TMPDIR_ROOT/config/crush" "$TMPDIR_ROOT/data/crush"

# ---------------------------------------------------------------------------
# Mock LLM server
#
# Tracks conversation turns and gives distinct responses per turn.
# Each ACP run triggers one LLM request (no tool calls for simplicity).
# ---------------------------------------------------------------------------
MOCK_SCRIPT="$TMPDIR_ROOT/mock_llm.py"
cat > "$MOCK_SCRIPT" << 'PYTHON_EOF'
import json, sys, time
from http.server import HTTPServer, BaseHTTPRequestHandler

PORT = int(sys.argv[1])

RESPONSES = [
    "internal",
    "Hello! I'm Crush, your AI coding assistant. How can I help you today?",
    "I can see this project has a Go module structure. The main packages are under cmd/ and the plugins are organized as separate modules. What would you like to work on?",
    "Sure thing! I've taken a look at the codebase. The test coverage looks solid with both unit and e2e tests for every plugin. Is there anything specific you'd like me to review?",
    "Goodbye! Feel free to reach out anytime you need help with your code.",
]

turn = [0]

def make_id():
    return "chatcmpl-%d" % int(time.time() * 1000)

def build_stream(text):
    cid = make_id()
    ts = int(time.time())
    chunks = []
    # Stream the text in small pieces for realistic behavior.
    words = text.split(" ")
    # Role chunk.
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts,
        "model": "mock-model",
        "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}],
    }))
    for i, word in enumerate(words):
        token = word if i == 0 else " " + word
        chunks.append("data: %s" % json.dumps({
            "id": cid, "object": "chat.completion.chunk", "created": ts,
            "model": "mock-model",
            "choices": [{"index": 0, "delta": {"content": token}, "finish_reason": None}],
        }))
    # Finish chunk.
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts,
        "model": "mock-model",
        "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
    }))
    chunks.append("data: [DONE]")
    return "\n\n".join(chunks) + "\n\n"

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *a):
        pass
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        is_stream = body.get("stream", False)

        idx = min(turn[0], len(RESPONSES) - 1)
        text = RESPONSES[idx]
        turn[0] += 1

        sys.stderr.write("LLM turn=%d stream=%s text=%s\n" % (idx, is_stream, text[:60]))
        sys.stderr.flush()

        if is_stream:
            resp_body = build_stream(text).encode()
            content_type = "text/event-stream"
        else:
            resp_body = json.dumps({
                "id": make_id(), "object": "chat.completion",
                "created": int(time.time()), "model": "mock-model",
                "choices": [{"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
            }).encode()
            content_type = "application/json"

        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(resp_body)))
        if content_type == "text/event-stream":
            self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        self.wfile.write(resp_body)
        self.wfile.flush()

HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
PYTHON_EOF

log "Starting mock LLM on port $LLM_PORT..."
python3 "$MOCK_SCRIPT" "$LLM_PORT" 2>"$TMPDIR_ROOT/mock_llm.log" &
PIDS+=($!)
sleep 0.3
pass "Mock LLM started (PID ${PIDS[-1]})"

# ---------------------------------------------------------------------------
# Crush config
# ---------------------------------------------------------------------------
cat > "$TMPDIR_ROOT/config/crush/crush.json" << CONF_EOF
{
  "providers": {
    "mock": {
      "type": "openai-compat",
      "base_url": "http://127.0.0.1:${LLM_PORT}",
      "api_key": "mock-key",
      "models": [{
        "id": "mock-model", "name": "Mock Model",
        "context_window": 128000, "default_max_tokens": 4096,
        "can_reason": false, "supports_attachments": false
      }]
    }
  },
  "models": {
    "large": {"provider": "mock", "model": "mock-model"},
    "small": {"provider": "mock", "model": "mock-model"}
  },
  "options": {
    "disabled_plugins": ["otlp","agent-status","periodic-prompts","subagents","tempotown","ping","tavily"],
    "plugins": {
      "acp-server": {
        "port": ${ACP_PORT},
        "agent_name": "crush",
        "description": "Integration test agent"
      }
    }
  }
}
CONF_EOF
cp "$TMPDIR_ROOT/config/crush/crush.json" "$TMPDIR_ROOT/data/crush/crush.json"

# ---------------------------------------------------------------------------
# Start Crush in serve mode
# ---------------------------------------------------------------------------
log "Starting Crush ACP server on port $ACP_PORT..."

env -i \
    PATH="$PATH" \
    HOME="$TMPDIR_ROOT" \
    XDG_CONFIG_HOME="$TMPDIR_ROOT/config" \
    XDG_DATA_HOME="$TMPDIR_ROOT/data" \
    TERM=dumb \
    CRUSH_DISABLE_METRICS=1 \
    "$CRUSH_BINARY" serve --verbose --cwd "$WORK_DIR" \
    >"$TMPDIR_ROOT/crush.log" 2>&1 &
PIDS+=($!)
CRUSH_PID=${PIDS[-1]}
log "Crush PID=$CRUSH_PID"

# Wait for ACP server readiness.
MAX_WAIT=30
i=0
while ! curl -sf "http://127.0.0.1:$ACP_PORT/ping" 2>/dev/null | grep -q "pong"; do
    sleep 0.5
    i=$((i + 1))
    if [[ $i -ge $MAX_WAIT ]]; then
        fail "Timeout waiting for Crush ACP server on port $ACP_PORT"
        log "=== Crush log (last 30 lines) ==="
        tail -30 "$TMPDIR_ROOT/crush.log" 2>/dev/null || true
        exit 1
    fi
done
pass "Crush ACP server ready on port $ACP_PORT"

# ---------------------------------------------------------------------------
# Verify agent discovery via acp-client
# ---------------------------------------------------------------------------
log "Verifying agent discovery..."
AGENTS_OUTPUT=$("$ACP_CLIENT" -url "http://127.0.0.1:$ACP_PORT" -once <<< "" 2>&1 || true)
# The agent auto-detection happens on connect — if it connects, it found the agent.
# Verify with curl as well.
AGENT_NAME=$(curl -sf "http://127.0.0.1:$ACP_PORT/agents" | python3 -c "import json,sys; print(json.load(sys.stdin)['agents'][0]['name'])" 2>/dev/null)
if [[ "$AGENT_NAME" == "crush" ]]; then
    pass "Agent 'crush' discovered"
else
    fail "Expected agent 'crush', got '$AGENT_NAME'"
    exit 1
fi

# ---------------------------------------------------------------------------
# 3-message conversation via acp-client
# ---------------------------------------------------------------------------
log ""
log "=========================================="
log "  3-Message Conversation Test"
log "=========================================="
log ""

ERRORS=0
CONVERSATION_LOG="$TMPDIR_ROOT/conversation.log"
: > "$CONVERSATION_LOG"

send_message() {
    local msg_num=$1
    local prompt=$2
    local expect_substr=$3

    log "Message $msg_num: Sending: \"$prompt\""

    local output
    output=$(echo "$prompt" | "$ACP_CLIENT" -url "http://127.0.0.1:$ACP_PORT" 2>>"$CONVERSATION_LOG")
    local exit_code=$?

    echo "--- Message $msg_num ---" >> "$CONVERSATION_LOG"
    echo "Prompt: $prompt" >> "$CONVERSATION_LOG"
    echo "Response: $output" >> "$CONVERSATION_LOG"
    echo "" >> "$CONVERSATION_LOG"

    if [[ $exit_code -ne 0 ]]; then
        fail "Message $msg_num: acp-client exited with code $exit_code"
        ERRORS=$((ERRORS + 1))
        return
    fi

    if [[ -z "$output" ]]; then
        fail "Message $msg_num: empty response"
        ERRORS=$((ERRORS + 1))
        return
    fi

    log "Message $msg_num: Response: \"${output:0:100}...\""

    if [[ -n "$expect_substr" ]] && ! echo "$output" | grep -qi "$expect_substr"; then
        warn "Message $msg_num: expected '$expect_substr' in response"
        warn "  Got: $output"
    fi

    pass "Message $msg_num: Got response (${#output} chars)"
}

send_message 1 "Hello, what can you do?" "Hello"
send_message 2 "Tell me about this project's structure" "module\|packages\|organized"
send_message 3 "How is the test coverage?" "test\|coverage\|solid\|review"

# ---------------------------------------------------------------------------
# Validate results
# ---------------------------------------------------------------------------
log ""
log "=========================================="
log "  Results"
log "=========================================="

# Verify the mock LLM was actually called.
LLM_CALLS=$(grep -c "^LLM turn=" "$TMPDIR_ROOT/mock_llm.log" 2>/dev/null || echo "0")
log "Mock LLM received $LLM_CALLS requests"

if [[ "$LLM_CALLS" -ge 3 ]]; then
    pass "Mock LLM received at least 3 requests"
else
    fail "Mock LLM received only $LLM_CALLS requests (expected >= 3)"
    ERRORS=$((ERRORS + 1))
fi

# Verify Crush stayed alive throughout.
if kill -0 "$CRUSH_PID" 2>/dev/null; then
    pass "Crush server still running"
else
    fail "Crush server died during test"
    ERRORS=$((ERRORS + 1))
fi

if [[ $ERRORS -eq 0 ]]; then
    log ""
    pass "=== ACP CLIENT INTEGRATION TEST PASSED ==="
else
    log ""
    fail "=== ACP CLIENT INTEGRATION TEST FAILED ($ERRORS errors) ==="
    log ""
    log "=== Debug logs ==="
    log "--- Conversation ---"
    cat "$CONVERSATION_LOG" 2>/dev/null || true
    log "--- Mock LLM ---"
    cat "$TMPDIR_ROOT/mock_llm.log" 2>/dev/null || true
    log "--- Crush (last 40 lines) ---"
    tail -40 "$TMPDIR_ROOT/crush.log" 2>/dev/null || true
fi

exit $ERRORS
