#!/usr/bin/env bash
#
# ACP Tic-Tac-Toe Integration Test
#
# Tests the ACP server & client by running a 5x5 tic-tac-toe game between
# two Crush instances acting as ACP servers (players), orchestrated by
# a game master that calls them via curl.
#
# Architecture:
#   - Player X: Crush ACP server on port 8201 (mock LLM on 9201)
#   - Player O: Crush ACP server on port 8202 (mock LLM on 9202)
#   - Game Master: bash loop sending POST /runs to each player
#
# Each player's mock LLM returns tool calls to read the board (view),
# place their mark (bash + sed), and respond with confirmation text.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
CRUSH_BINARY="$PROJECT_DIR/dist/crush"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${CYAN}[test]${NC} $*"; }
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

PIDS=()
TMPDIRS=()
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
    for d in "${TMPDIRS[@]}"; do
        rm -rf "$d"
    done
    log "Done."
}
trap cleanup EXIT

if [[ ! -x "$CRUSH_BINARY" ]]; then
    fail "Crush binary not found at $CRUSH_BINARY — run 'task distro:all' first"
    exit 1
fi
if ! "$CRUSH_BINARY" --list-plugins 2>/dev/null | grep -q "acp-server"; then
    fail "acp-server hook not registered in crush binary"
    exit 1
fi
pass "Crush binary found with acp-server hook"

###############################################################################
# Game workspace
###############################################################################
GAME_DIR=$(mktemp -d)
TMPDIRS+=("$GAME_DIR")
BOARD_FILE="$GAME_DIR/board.txt"

cat > "$BOARD_FILE" << 'BOARD'
  1 2 3 4 5
A . . . . .
B . . . . .
C . . . . .
D . . . . .
E . . . . .
BOARD
log "Board initialized at $BOARD_FILE"
cat "$BOARD_FILE"



###############################################################################
# Mock LLM server (Python)
#
# Stateless: determines the phase from the message history in each request.
# Phase 1 (no tool results): return view tool call to read the board.
# Phase 2 (has view result, no bash result): return bash tool call to place mark.
# Phase 3 (has bash result): return text confirming the move.
###############################################################################
create_mock_llm() {
    local port=$1
    local player_mark=$2
    local mock_script="$GAME_DIR/mock_llm_${player_mark}.py"

    cat > "$mock_script" << PYTHON_EOF
import json, sys, time
from http.server import HTTPServer, BaseHTTPRequestHandler

PORT = ${port}
MARK = "${player_mark}"
BOARD_FILE = "${BOARD_FILE}"

if MARK == "X":
    MOVES = [("C",3),("C",2),("C",4),("B",3),("D",3),("C",1),("C",5),("A",3),("E",3),("B",2),("D",4),("A",1),("E",5)]
else:
    MOVES = [("B",2),("D",4),("B",4),("D",2),("A",1),("E",5),("A",5),("E",1),("B",1),("D",1),("A",2),("E",2),("B",5)]

move_idx = [0]
call_ctr = [0]

def next_id():
    call_ctr[0] += 1
    return "call_%s_%d" % (MARK, call_ctr[0])

def find_move(board_text):
    taken = set()
    for line in board_text.strip().split("\\n"):
        line = line.strip()
        if not line or line[0] == " " or line[0] == "<":
            continue
        parts = line.split()
        if len(parts) == 6:
            row = parts[0]
            for ci, cell in enumerate(parts[1:], 1):
                if cell != ".":
                    taken.add((row, ci))
    while move_idx[0] < len(MOVES):
        m = MOVES[move_idx[0]]
        move_idx[0] += 1
        if m not in taken:
            return m
    for r in "ABCDE":
        for c in range(1, 6):
            if (r, c) not in taken:
                return (r, c)
    return None

def make_chunk_id():
    return "chatcmpl-%s-%d" % (MARK, int(time.time() * 1000))

def build_text_stream(text, finish="stop"):
    cid = make_chunk_id()
    ts = int(time.time())
    chunks = []
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts, "model": "mock-model",
        "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}],
    }))
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts, "model": "mock-model",
        "choices": [{"index": 0, "delta": {"content": text}, "finish_reason": None}],
    }))
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts, "model": "mock-model",
        "choices": [{"index": 0, "delta": {}, "finish_reason": finish}],
        "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
    }))
    chunks.append("data: [DONE]")
    return "\\n\\n".join(chunks) + "\\n\\n"

def build_tool_call_stream(tc_id, name, arguments):
    cid = make_chunk_id()
    ts = int(time.time())
    chunks = []
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts, "model": "mock-model",
        "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}],
    }))
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts, "model": "mock-model",
        "choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "id": tc_id, "type": "function", "function": {"name": name, "arguments": arguments}}]}, "finish_reason": None}],
    }))
    chunks.append("data: %s" % json.dumps({
        "id": cid, "object": "chat.completion.chunk", "created": ts, "model": "mock-model",
        "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}],
        "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
    }))
    chunks.append("data: [DONE]")
    return "\\n\\n".join(chunks) + "\\n\\n"

class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *a): pass
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        msgs = body.get("messages", [])
        is_stream = body.get("stream", False)

        tools = body.get("tools", [])
        tool_names = [t.get("function",{}).get("name","") for t in tools]
        sys.stderr.write("REQ stream=%s tools=%s msg_count=%d\\n" % (is_stream, tool_names[:5], len(msgs)))
        sys.stderr.flush()

        resp_body = None
        content_type = "application/json"

        if not tools:
            if is_stream:
                resp_body = build_text_stream("Tic-Tac-Toe Move").encode()
                content_type = "text/event-stream"
            else:
                resp_body = json.dumps({
                    "id": make_chunk_id(), "object": "chat.completion", "created": int(time.time()), "model": "mock-model",
                    "choices": [{"index": 0, "message": {"role": "assistant", "content": "Tic-Tac-Toe Move"}, "finish_reason": "stop"}],
                    "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
                }).encode()
        else:
            tool_results = [m for m in msgs if m.get("role") == "tool"]
            num_tool_results = len(tool_results)
            sys.stderr.write("AGENT phase=%d\\n" % (num_tool_results + 1))
            sys.stderr.flush()

            if num_tool_results >= 2:
                if is_stream:
                    resp_body = build_text_stream("I placed %s on the board. Move complete." % MARK).encode()
                    content_type = "text/event-stream"
                else:
                    resp_body = json.dumps({
                        "id": make_chunk_id(), "object": "chat.completion", "created": int(time.time()), "model": "mock-model",
                        "choices": [{"index": 0, "message": {"role": "assistant", "content": "I placed %s on the board." % MARK}, "finish_reason": "stop"}],
                        "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
                    }).encode()
            elif num_tool_results == 1:
                board = tool_results[0].get("content", "")
                mv = find_move(board)
                if mv is None:
                    if is_stream:
                        resp_body = build_text_stream("Board full.").encode()
                        content_type = "text/event-stream"
                    else:
                        resp_body = json.dumps({
                            "id": make_chunk_id(), "object": "chat.completion", "created": int(time.time()), "model": "mock-model",
                            "choices": [{"index": 0, "message": {"role": "assistant", "content": "Board full."}, "finish_reason": "stop"}],
                            "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
                        }).encode()
                else:
                    row, col = mv
                    pos = 2 * col + 1
                    cmd = "sed -i '/^%s /s/./%s/%d' %s && cat %s" % (row, MARK, pos, BOARD_FILE, BOARD_FILE)
                    args = json.dumps({"command": cmd, "description": "Place %s at %s%d" % (MARK, row, col)})
                    sys.stderr.write("move: %s%d cmd=%s\\n" % (row, col, cmd))
                    sys.stderr.flush()
                    if is_stream:
                        resp_body = build_tool_call_stream(next_id(), "bash", args).encode()
                        content_type = "text/event-stream"
                    else:
                        resp_body = json.dumps({
                            "id": make_chunk_id(), "object": "chat.completion", "created": int(time.time()), "model": "mock-model",
                            "choices": [{"index": 0, "message": {"role": "assistant", "tool_calls": [{"id": next_id(), "type": "function", "function": {"name": "bash", "arguments": args}}]}, "finish_reason": "tool_calls"}],
                            "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
                        }).encode()
            else:
                args = json.dumps({"command": "cat " + BOARD_FILE, "description": "Read board"})
                if is_stream:
                    resp_body = build_tool_call_stream(next_id(), "bash", args).encode()
                    content_type = "text/event-stream"
                else:
                    resp_body = json.dumps({
                        "id": make_chunk_id(), "object": "chat.completion", "created": int(time.time()), "model": "mock-model",
                        "choices": [{"index": 0, "message": {"role": "assistant", "tool_calls": [{"id": next_id(), "type": "function", "function": {"name": "bash", "arguments": args}}]}, "finish_reason": "tool_calls"}],
                        "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
                    }).encode()

        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(resp_body)))
        if content_type == "text/event-stream":
            self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        self.wfile.write(resp_body)
        self.wfile.flush()

HTTPServer(("127.0.0.1", PORT), H).serve_forever()
PYTHON_EOF

    python3 "$mock_script" 2>"$GAME_DIR/mock_${player_mark}.log" &
    PIDS+=($!)
    log "Mock LLM for $player_mark on port $port (PID ${PIDS[-1]})"
}

###############################################################################
# Player environment setup
###############################################################################
setup_player_env() {
    local name=$1 acp_port=$2 llm_port=$3
    local d="$GAME_DIR/$name"
    mkdir -p "$d/config/crush" "$d/data/crush" "$d/work/.crush"
    touch "$d/work/.crush/init"

    cat > "$d/config/crush/crush.json" << CONF_EOF
{
  "providers": {
    "mock": {
      "type": "openai-compat",
      "base_url": "http://127.0.0.1:${llm_port}",
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
    "disabled_plugins": ["otlp","agent-status","periodic-prompts","subagents","tempotown","ping"],
    "plugins": {
      "acp-server": {
        "port": ${acp_port},
        "agent_name": "${name}",
        "description": "Tic-tac-toe ${name}"
      }
    }
  }
}
CONF_EOF
    cp "$d/config/crush/crush.json" "$d/data/crush/crush.json"
    echo "$d"
}

###############################################################################
# Start everything
###############################################################################
log "Starting mock LLMs..."
create_mock_llm 9201 "X"
create_mock_llm 9202 "O"
sleep 0.5
pass "Mock LLM servers started"

log "Setting up player environments..."
PLAYER_X_DIR=$(setup_player_env "player_x" 8201 9201)
PLAYER_O_DIR=$(setup_player_env "player_o" 8202 9202)
pass "Player environments created"

log "Starting Crush ACP servers..."

# Use `script` to provide a PTY for crush's interactive TUI.
# The TUI sits idle while the ACP hook serves requests in the background.
start_crush_with_pty() {
    local player_dir=$1 name=$2 acp_port=$3
    local log_file="$GAME_DIR/${name}.log"

    # Filter environment to avoid picking up real API keys.
    env -i \
        PATH="$PATH" \
        HOME="$player_dir" \
        XDG_CONFIG_HOME="$player_dir/config" \
        XDG_DATA_HOME="$player_dir/data" \
        TERM=xterm \
        CRUSH_DISABLE_METRICS=1 \
        script -qfc "$CRUSH_BINARY --cwd $player_dir/work" /dev/null \
        > "$log_file" 2>&1 &

    PIDS+=($!)
    log "Started $name (PID ${PIDS[-1]}, ACP :$acp_port, log: $log_file)"
}

start_crush_with_pty "$PLAYER_X_DIR" "player_x" 8201
start_crush_with_pty "$PLAYER_O_DIR" "player_o" 8202

# Wait for ACP servers.
wait_for_acp() {
    local port=$1 name=$2 max=30 i=0
    while ! curl -sf "http://127.0.0.1:$port/ping" 2>/dev/null | grep -q "pong"; do
        sleep 0.5
        i=$((i + 1))
        if [[ $i -ge $max ]]; then
            fail "Timeout waiting for $name on port $port"
            log "=== $name log ==="
            cat "$GAME_DIR/${name}.log" 2>/dev/null | head -50 || true
            exit 1
        fi
    done
    pass "$name ACP ready on :$port"
}

wait_for_acp 8201 "player_x"
wait_for_acp 8202 "player_o"

###############################################################################
# Verify agent discovery
###############################################################################
log "Verifying ACP agent discovery..."
for port_name in "8201:player_x" "8202:player_o"; do
    port="${port_name%%:*}"
    name="${port_name##*:}"
    agents=$(curl -sf "http://127.0.0.1:$port/agents")
    aname=$(echo "$agents" | python3 -c "import json,sys; print(json.load(sys.stdin)['agents'][0]['name'])" 2>/dev/null)
    if [[ "$aname" == "$name" ]]; then
        pass "Agent '$name' discovered on :$port"
    else
        fail "Expected agent '$name' on :$port, got '$aname'"
        exit 1
    fi
done

###############################################################################
# Game master — play tic-tac-toe
###############################################################################
log ""
log "=========================================="
log "  5x5 Tic-Tac-Toe via ACP"
log "=========================================="
log ""

submit_move() {
    local port=$1 agent=$2 mark=$3 turn=$4
    local prompt="You are player ${mark} in a 5x5 tic-tac-toe game. The board file is ${BOARD_FILE}. Read it with the view tool, then use bash with sed to place your mark '${mark}' on an empty cell (shown as '.'). Do NOT modify the header row."

    log "Turn $turn: $agent ($mark) making a move..."

    local escaped_prompt
    escaped_prompt=$(echo "$prompt" | python3 -c "import json,sys; print(json.dumps(sys.stdin.read().strip()))")

    local response
    response=$(curl -sf --max-time 30 -X POST "http://127.0.0.1:$port/runs" \
        -H "Content-Type: application/json" \
        -d '{
            "agent_name": "'"$agent"'",
            "input": [{"role": "user", "parts": [{"type": "text", "content_type": "text/plain", "content": '"$escaped_prompt"'}]}],
            "mode": "sync"
        }' 2>&1)

    local status
    status=$(echo "$response" | python3 -c "import json,sys; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || echo "error")

    if [[ "$status" == "completed" ]]; then
        pass "Turn $turn: $mark move completed"
        echo "$response" | python3 -c "
import json, sys
r = json.load(sys.stdin)
output = r.get('output', [])
for msg in output:
    role = msg.get('role', '?')
    parts = msg.get('parts', [])
    for p in parts:
        ct = p.get('type', '?')
        content = p.get('content', '')[:200]
        print('  %s [%s]: %s' % (role, ct, content))
" 2>/dev/null || true
    else
        warn "Turn $turn: status=$status"
        echo "$response" | python3 -m json.tool 2>/dev/null | head -20 || echo "$response" | head -5
    fi

    echo ""
    cat "$BOARD_FILE"
    echo ""
}

MAX_TURNS=${TEST_TURNS:-8}
for turn in $(seq 1 $MAX_TURNS); do
    if (( turn % 2 == 1 )); then
        submit_move 8201 "player_x" "X" "$turn" || { fail "Turn $turn failed"; break; }
    else
        submit_move 8202 "player_o" "O" "$turn" || { fail "Turn $turn failed"; break; }
    fi
    sleep 0.3
done



###############################################################################
# Validate
###############################################################################
log ""
log "=========================================="
log "  Final Board"
log "=========================================="
cat "$BOARD_FILE"
echo ""

X_COUNT=$(tr -cd 'X' < "$BOARD_FILE" | wc -c)
O_COUNT=$(tr -cd 'O' < "$BOARD_FILE" | wc -c)
DOT_COUNT=$(tr -cd '.' < "$BOARD_FILE" | wc -c)
TOTAL=$((X_COUNT + O_COUNT + DOT_COUNT))

log "X=$X_COUNT  O=$O_COUNT  empty=$DOT_COUNT  total=$TOTAL"

ERRORS=0
if [[ $X_COUNT -lt 1 ]] || [[ $O_COUNT -lt 1 ]]; then
    fail "Both players should have marks (X=$X_COUNT, O=$O_COUNT)"
    ERRORS=1
else
    pass "Both players placed marks"
fi

EXPECTED_X=$(( (MAX_TURNS + 1) / 2 ))
EXPECTED_O=$(( MAX_TURNS / 2 ))
if [[ $X_COUNT -eq $EXPECTED_X ]] && [[ $O_COUNT -eq $EXPECTED_O ]]; then
    pass "Mark counts correct: X=$X_COUNT/$EXPECTED_X  O=$O_COUNT/$EXPECTED_O"
else
    warn "Mark counts: X=$X_COUNT/$EXPECTED_X  O=$O_COUNT/$EXPECTED_O"
fi

if [[ $TOTAL -eq 25 ]]; then
    pass "Board integrity: $TOTAL cells"
else
    fail "Board integrity: $TOTAL cells (expected 25)"
    ERRORS=1
fi

if [[ $ERRORS -eq 0 ]]; then
    log ""
    pass "=== ACP TIC-TAC-TOE TEST PASSED ==="
else
    log ""
    fail "=== ACP TIC-TAC-TOE TEST FAILED ==="
fi

if [[ $ERRORS -ne 0 ]]; then
    log ""
    log "=== Debug logs ==="
    log "Mock X:"
    cat "$GAME_DIR/mock_X.log" 2>/dev/null || true
    log "Mock O:"
    cat "$GAME_DIR/mock_O.log" 2>/dev/null || true
    log "Player X (last 20 lines):"
    tail -20 "$GAME_DIR/player_x.log" 2>/dev/null || true
    log "Player O (last 20 lines):"
    tail -20 "$GAME_DIR/player_o.log" 2>/dev/null || true
fi

exit $ERRORS
