import io

from crush_acp_sdk.stream import parse_stream_sync


def test_parse_basic_ndjson():
    stream = io.StringIO(
        '{"type":"run.created","run":{"agent_name":"echo","run_id":"r1","session_id":"","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}\n'
        '{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}\n'
        '{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","session_id":"","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}\n'
    )
    events = list(parse_stream_sync(stream))
    assert len(events) == 3
    assert events[0].type == "run.created"
    assert events[1].type == "message.part"
    assert events[1].part is not None
    assert events[1].part.content == "Hello"
    assert events[2].type == "run.completed"


def test_parse_skips_empty_lines():
    stream = io.StringIO(
        '{"type":"message.part","part":{"content_type":"text/plain","content":"ok"}}\n'
        "\n"
        '{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","session_id":"","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}\n'
    )
    events = list(parse_stream_sync(stream))
    assert len(events) == 2


def test_parse_invalid_json():
    stream = io.StringIO("not-valid-json\n")
    events = list(parse_stream_sync(stream))
    assert len(events) == 1
    assert events[0].type == "error"
    assert events[0].error is not None
    assert "failed to parse event" in events[0].error.message


def test_parse_empty_stream():
    stream = io.StringIO("")
    events = list(parse_stream_sync(stream))
    assert len(events) == 0


def test_parse_detects_sse_data():
    stream = io.StringIO('data: {"type":"message.part"}\n')
    events = list(parse_stream_sync(stream))
    assert len(events) == 1
    assert events[0].type == "error"
    assert "server sent SSE-formatted data instead of NDJSON" in events[0].error.message
    assert "application/x-ndjson" in events[0].error.message


def test_parse_detects_sse_event():
    stream = io.StringIO("event: message\n")
    events = list(parse_stream_sync(stream))
    assert len(events) == 1
    assert "SSE-formatted" in events[0].error.message


def test_parse_detects_done():
    stream = io.StringIO("[DONE]\n")
    events = list(parse_stream_sync(stream))
    assert len(events) == 1
    assert "SSE-formatted" in events[0].error.message
