from __future__ import annotations

import json
import time
from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from typing import Any

import httpx

from crush_acp_sdk.stream import parse_stream, _dict_to_event, _dict_to_run
from crush_acp_sdk.types import (
    AcpError,
    AgentManifest,
    Event,
    Message,
    MessagePart,
    Run,
    SessionData,
    SessionMessage,
    SessionSnapshot,
    new_user_message,
    text_content,
)

CONTENT_TYPE_NDJSON = "application/x-ndjson"


@dataclass
class ClientOptions:
    headers: dict[str, str] = field(default_factory=dict)
    agent_name: str | None = None
    timeout: float = 0


@dataclass
class SessionResult:
    run: Run | None = None
    snapshot: SessionSnapshot | None = None

    def text(self) -> str:
        if self.run is None:
            return ""
        return text_content(self.run.output)


class Stream:
    def __init__(self, response: httpx.Response) -> None:
        self._response = response
        self._last_run: Run | None = None
        self._snapshot: SessionSnapshot | None = None
        self._err: Exception | None = None

    async def events(self) -> AsyncIterator[Event]:
        async for event in parse_stream(self._response.aiter_lines()):
            if event.run is not None:
                self._last_run = event.run
            if event.type == "session.snapshot" and event.generic is not None:
                snap = _try_decode_snapshot(event.generic)
                if snap is not None:
                    self._snapshot = snap
            yield event

    async def result(self) -> SessionResult:
        async for _ in self.events():
            pass
        return SessionResult(run=self._last_run, snapshot=self._snapshot)

    @property
    def err(self) -> Exception | None:
        return self._err


def _try_decode_snapshot(data: Any) -> SessionSnapshot | None:
    if not isinstance(data, dict):
        return None
    version = data.get("version", 0)
    if not version:
        return None
    session_data = data.get("session", {})
    session = SessionData(
        id=session_data.get("id", ""),
        title=session_data.get("title", ""),
        message_count=session_data.get("message_count", 0),
        prompt_tokens=session_data.get("prompt_tokens", 0),
        completion_tokens=session_data.get("completion_tokens", 0),
        cost=session_data.get("cost", 0.0),
        created_at=session_data.get("created_at", 0),
        updated_at=session_data.get("updated_at", 0),
    )
    messages = []
    for m in data.get("messages", []):
        messages.append(
            SessionMessage(
                id=m.get("id", ""),
                session_id=m.get("session_id", ""),
                role=m.get("role", ""),
                parts=m.get("parts", ""),
                model=m.get("model"),
                provider=m.get("provider"),
                is_summary_message=m.get("is_summary_message", False),
                created_at=m.get("created_at", 0),
                updated_at=m.get("updated_at", 0),
            )
        )
    return SessionSnapshot(version=version, session=session, messages=messages)


class AcpClientError(Exception):
    def __init__(self, status: int, message: str) -> None:
        super().__init__(f"ACP error (HTTP {status}): {message}")
        self.status = status


class Client:
    def __init__(self, base_url: str, options: ClientOptions | None = None) -> None:
        opts = options or ClientOptions()
        self._base_url = base_url
        self._headers = dict(opts.headers)
        self._agent_name: str | None = opts.agent_name
        timeout = httpx.Timeout(opts.timeout if opts.timeout > 0 else None)
        self._client = httpx.AsyncClient(headers=self._headers, timeout=timeout)

    async def __aenter__(self) -> "Client":
        return self

    async def __aexit__(self, *args: Any) -> None:
        await self._client.aclose()

    async def ping(self) -> None:
        resp = await self._client.get(f"{self._base_url}/ping")
        if resp.status_code != 200 or resp.text != "pong":
            raise RuntimeError(
                f"ping: unexpected response (HTTP {resp.status_code}): {resp.text}"
            )

    async def list_agents(self) -> list[AgentManifest]:
        resp = await self._client.get(f"{self._base_url}/agents")
        if resp.status_code != 200:
            raise self._read_error(resp)
        data = resp.json()
        return [
            AgentManifest(name=a["name"], description=a.get("description"))
            for a in data.get("agents", [])
        ]

    async def new_session(self, prompt: str) -> SessionResult:
        return await self._run_sync(None, prompt)

    async def resume(self, session_id: str, prompt: str) -> SessionResult:
        if not session_id:
            raise ValueError("session ID is required for resume")
        return await self._run_sync(session_id, prompt)

    async def new_session_stream(self, prompt: str) -> Stream:
        return await self._run_stream(None, prompt)

    async def resume_stream(self, session_id: str, prompt: str) -> Stream:
        if not session_id:
            raise ValueError("session ID is required for resume_stream")
        return await self._run_stream(session_id, prompt)

    async def dump(self, session_id: str) -> SessionSnapshot:
        resp = await self._client.get(
            f"{self._base_url}/sessions/{session_id}/export"
        )
        if resp.status_code != 200:
            raise self._read_error(resp)
        data = resp.json()
        snap = _try_decode_snapshot(data)
        if snap is None:
            raise RuntimeError("invalid snapshot response")
        return snap

    async def restore(self, snapshot: SessionSnapshot) -> None:
        body = {
            "version": snapshot.version,
            "session": {
                "id": snapshot.session.id,
                "title": snapshot.session.title,
                "message_count": snapshot.session.message_count,
                "prompt_tokens": snapshot.session.prompt_tokens,
                "completion_tokens": snapshot.session.completion_tokens,
                "cost": snapshot.session.cost,
                "created_at": snapshot.session.created_at,
                "updated_at": snapshot.session.updated_at,
            },
            "messages": [
                {
                    "id": m.id,
                    "session_id": m.session_id,
                    "role": m.role,
                    "parts": m.parts,
                    "model": m.model,
                    "provider": m.provider,
                    "is_summary_message": m.is_summary_message,
                    "created_at": m.created_at,
                    "updated_at": m.updated_at,
                }
                for m in snapshot.messages
            ],
        }
        resp = await self._client.post(
            f"{self._base_url}/sessions/import", json=body
        )
        if resp.status_code != 200:
            raise self._read_error(resp)
        data = resp.json()
        if data.get("status") != "imported":
            raise RuntimeError(f"unexpected import status: {data.get('status')}")

    async def wait_ready(
        self, interval: float = 0.5, timeout: float = 30.0
    ) -> None:
        deadline = time.monotonic() + timeout
        while True:
            try:
                await self.ping()
                return
            except Exception:
                if time.monotonic() >= deadline:
                    raise RuntimeError("server not ready: timeout")
                await _async_sleep(interval)

    async def _resolve_agent(self) -> str:
        if self._agent_name:
            return self._agent_name
        agents = await self.list_agents()
        if not agents:
            raise RuntimeError(f"no agents available on {self._base_url}")
        self._agent_name = agents[0].name
        return self._agent_name

    async def _run_sync(
        self, session_id: str | None, prompt: str
    ) -> SessionResult:
        agent = await self._resolve_agent()
        body: dict[str, Any] = {
            "agent_name": agent,
            "input": [_message_to_dict(new_user_message(prompt))],
            "mode": "sync",
        }
        if session_id:
            body["session_id"] = session_id

        resp = await self._client.post(f"{self._base_url}/runs", json=body)
        if resp.status_code not in (200, 202):
            raise self._read_error(resp)
        run = _dict_to_run(resp.json())
        return SessionResult(run=run)

    async def _run_stream(
        self, session_id: str | None, prompt: str
    ) -> Stream:
        agent = await self._resolve_agent()
        body: dict[str, Any] = {
            "agent_name": agent,
            "input": [_message_to_dict(new_user_message(prompt))],
            "mode": "stream",
        }
        if session_id:
            body["session_id"] = session_id

        req = self._client.build_request(
            "POST",
            f"{self._base_url}/runs",
            json=body,
            headers={"Accept": CONTENT_TYPE_NDJSON},
        )
        resp = await self._client.send(req, stream=True)
        if resp.status_code != 200:
            await resp.aread()
            raise self._read_error(resp)
        return Stream(resp)

    @staticmethod
    def _read_error(resp: httpx.Response) -> AcpClientError:
        try:
            data = resp.json()
            msg = data.get("message", "")
            if msg:
                return AcpClientError(resp.status_code, msg)
        except Exception:
            pass
        return AcpClientError(resp.status_code, resp.text)


def _message_to_dict(msg: Message) -> dict:
    return {
        "role": msg.role,
        "parts": [{"content_type": p.content_type, "content": p.content} for p in msg.parts],
    }


async def _async_sleep(seconds: float) -> None:
    import asyncio
    await asyncio.sleep(seconds)
