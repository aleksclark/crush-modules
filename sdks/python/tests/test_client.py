import json

import httpx
import pytest

from crush_acp_sdk.client import Client, ClientOptions, SessionResult


def _mock_transport(handler):
    """Create an httpx transport that routes to the given handler function."""

    class _Transport(httpx.AsyncBaseTransport):
        async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
            return await handler(request)

    return _Transport()


@pytest.fixture
def make_client():
    """Factory for creating a Client with a mock transport."""

    def _make(handler, **kwargs):
        transport = _mock_transport(handler)
        opts = ClientOptions(**kwargs)
        client = Client.__new__(Client)
        client._base_url = "http://test"
        client._headers = dict(opts.headers or {})
        client._agent_name = opts.agent_name
        client._client = httpx.AsyncClient(transport=transport, headers=client._headers)
        return client

    return _make


@pytest.mark.asyncio
async def test_ping(make_client):
    async def handler(req):
        return httpx.Response(200, text="pong")

    client = make_client(handler)
    await client.ping()


@pytest.mark.asyncio
async def test_ping_error(make_client):
    async def handler(req):
        return httpx.Response(503, text="not ready")

    client = make_client(handler)
    with pytest.raises(RuntimeError, match="unexpected response"):
        await client.ping()


@pytest.mark.asyncio
async def test_list_agents(make_client):
    async def handler(req):
        return httpx.Response(
            200,
            json={"agents": [{"name": "crush", "description": "Crush AI"}]},
        )

    client = make_client(handler)
    agents = await client.list_agents()
    assert len(agents) == 1
    assert agents[0].name == "crush"


@pytest.mark.asyncio
async def test_new_session(make_client):
    async def handler(req):
        if req.url.path == "/agents":
            return httpx.Response(200, json={"agents": [{"name": "crush"}]})

        body = json.loads(req.content)
        assert body["agent_name"] == "crush"
        assert body["mode"] == "sync"

        return httpx.Response(
            200,
            json={
                "agent_name": "crush",
                "run_id": "run-1",
                "session_id": "ses-abc",
                "status": "completed",
                "output": [
                    {
                        "role": "agent",
                        "parts": [{"content_type": "text/plain", "content": "Hi there!"}],
                    }
                ],
                "created_at": "2025-01-01T00:00:00Z",
            },
        )

    client = make_client(handler)
    result = await client.new_session("hello")
    assert result.run is not None
    assert result.run.session_id == "ses-abc"
    assert result.run.status == "completed"
    assert result.text() == "Hi there!"


@pytest.mark.asyncio
async def test_resume_requires_session_id(make_client):
    async def handler(req):
        return httpx.Response(200, text="pong")

    client = make_client(handler)
    with pytest.raises(ValueError, match="session ID is required"):
        await client.resume("", "hello")


@pytest.mark.asyncio
async def test_dump(make_client):
    async def handler(req):
        return httpx.Response(
            200,
            json={
                "version": 1,
                "session": {
                    "id": "ses-abc",
                    "title": "Test",
                    "message_count": 1,
                    "prompt_tokens": 0,
                    "completion_tokens": 0,
                    "cost": 0.0,
                    "created_at": 0,
                    "updated_at": 0,
                },
                "messages": [
                    {
                        "id": "m1",
                        "session_id": "ses-abc",
                        "role": "user",
                        "parts": "[]",
                        "created_at": 0,
                        "updated_at": 0,
                    }
                ],
            },
        )

    client = make_client(handler)
    snap = await client.dump("ses-abc")
    assert snap.version == 1
    assert snap.session.id == "ses-abc"
    assert len(snap.messages) == 1


@pytest.mark.asyncio
async def test_restore(make_client):
    async def handler(req):
        return httpx.Response(
            200,
            json={"session_id": "ses-abc", "message_count": 0, "status": "imported"},
        )

    from crush_acp_sdk.types import SessionData, SessionSnapshot

    client = make_client(handler)
    snap = SessionSnapshot(version=1, session=SessionData(id="ses-abc"), messages=[])
    await client.restore(snap)


@pytest.mark.asyncio
async def test_restore_error(make_client):
    async def handler(req):
        return httpx.Response(
            400,
            json={"code": 400, "message": "snapshot version is required"},
        )

    from crush_acp_sdk.types import SessionData, SessionSnapshot

    client = make_client(handler)
    snap = SessionSnapshot(version=0, session=SessionData(), messages=[])
    with pytest.raises(Exception, match="snapshot version is required"):
        await client.restore(snap)


@pytest.mark.asyncio
async def test_new_session_stream(make_client):
    events_ndjson = (
        '{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}\n'
        '{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}\n'
        '{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}\n'
        '{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"Hello World"}]}],"created_at":"2025-01-01T00:00:00Z"}}\n'
    )

    async def handler(req):
        if req.url.path == "/agents":
            return httpx.Response(200, json={"agents": [{"name": "crush"}]})
        return httpx.Response(
            200,
            headers={"content-type": "application/x-ndjson"},
            stream=httpx.ByteStream(events_ndjson.encode()),
        )

    client = make_client(handler)
    stream = await client.new_session_stream("hi")

    parts = []
    async for ev in stream.events():
        if ev.type == "message.part" and ev.part:
            parts.append(ev.part.content)

    assert parts == ["Hello", " World"]


@pytest.mark.asyncio
async def test_stream_result(make_client):
    events_ndjson = (
        '{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"done"}]}],"created_at":"2025-01-01T00:00:00Z"}}\n'
    )

    async def handler(req):
        if req.url.path == "/agents":
            return httpx.Response(200, json={"agents": [{"name": "crush"}]})
        return httpx.Response(
            200,
            headers={"content-type": "application/x-ndjson"},
            stream=httpx.ByteStream(events_ndjson.encode()),
        )

    client = make_client(handler)
    stream = await client.new_session_stream("hi")
    result = await stream.result()
    assert result.run is not None
    assert result.run.session_id == "ses-1"
    assert result.run.status == "completed"


@pytest.mark.asyncio
async def test_server_error(make_client):
    async def handler(req):
        return httpx.Response(500, json={"code": 500, "message": "internal error"})

    client = make_client(handler)
    with pytest.raises(Exception, match="internal error"):
        await client.list_agents()
