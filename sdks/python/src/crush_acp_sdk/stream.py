from __future__ import annotations

import json
from collections.abc import AsyncIterator, Iterator
from typing import IO

from crush_acp_sdk.types import AcpError, Event, MessagePart, Run

_SSE_PREFIXES = ("data:", "event:", "id:", "retry:", ":")


def _looks_like_sse(line: str) -> bool:
    if line == "[DONE]":
        return True
    return any(line.startswith(p) for p in _SSE_PREFIXES)


def _truncate(s: str, n: int) -> str:
    return s if len(s) <= n else s[:n] + "..."


def _make_sse_error(line: str) -> Event:
    return Event(
        type="error",
        error=AcpError(
            message=(
                f"server sent SSE-formatted data instead of NDJSON "
                f"(got line starting with {_truncate(line, 40)!r}) "
                f"\u2014 the ACP server must use streamable HTTP "
                f"(application/x-ndjson), not SSE (text/event-stream)"
            )
        ),
    )


def _parse_line(line: str) -> Event | None:
    if not line:
        return None
    if _looks_like_sse(line):
        return _make_sse_error(line)
    try:
        data = json.loads(line)
    except json.JSONDecodeError as e:
        return Event(type="error", error=AcpError(message=f"failed to parse event: {e}"))
    return _dict_to_event(data)


def _dict_to_event(d: dict) -> Event:
    event = Event(type=d.get("type", ""))
    if "run" in d and d["run"] is not None:
        event.run = _dict_to_run(d["run"])
    if "message" in d and d["message"] is not None:
        event.message = _dict_to_message(d["message"])
    if "part" in d and d["part"] is not None:
        event.part = _dict_to_part(d["part"])
    if "error" in d and d["error"] is not None:
        e = d["error"]
        event.error = AcpError(message=e.get("message", ""), code=e.get("code", 0), data=e.get("data"))
    if "generic" in d:
        event.generic = d["generic"]
    return event


def _dict_to_run(d: dict) -> Run:
    from crush_acp_sdk.types import AwaitRequest, Message as Msg
    run = Run(
        agent_name=d.get("agent_name", ""),
        run_id=d.get("run_id", ""),
        session_id=d.get("session_id", ""),
        status=d.get("status", "created"),
        created_at=d.get("created_at", ""),
        finished_at=d.get("finished_at"),
    )
    for msg_data in (d.get("output") or []):
        run.output.append(_dict_to_message(msg_data))
    if "error" in d and d["error"] is not None:
        e = d["error"]
        run.error = AcpError(message=e.get("message", ""), code=e.get("code", 0))
    if "await_request" in d and d["await_request"] is not None:
        ar = d["await_request"]
        req = AwaitRequest()
        if "message" in ar and ar["message"]:
            req.message = _dict_to_message(ar["message"])
        run.await_request = req
    return run


def _dict_to_message(d: dict):
    from crush_acp_sdk.types import Message
    msg = Message(role=d.get("role", ""), created_at=d.get("created_at"), completed_at=d.get("completed_at"))
    for p in d.get("parts") or []:
        msg.parts.append(_dict_to_part(p))
    return msg


def _dict_to_part(d: dict) -> MessagePart:
    return MessagePart(
        content_type=d.get("content_type", ""),
        content=d.get("content", ""),
        name=d.get("name"),
        content_encoding=d.get("content_encoding"),
        content_url=d.get("content_url"),
        metadata=d.get("metadata"),
    )


def parse_stream_sync(stream: IO[str]) -> Iterator[Event]:
    for line in stream:
        line = line.rstrip("\n").rstrip("\r")
        event = _parse_line(line)
        if event is not None:
            yield event


async def parse_stream(lines: AsyncIterator[str]) -> AsyncIterator[Event]:
    async for line in lines:
        line = line.rstrip("\n").rstrip("\r")
        event = _parse_line(line)
        if event is not None:
            yield event
