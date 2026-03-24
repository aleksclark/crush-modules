from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal

RunStatus = Literal[
    "created",
    "in-progress",
    "awaiting",
    "completed",
    "failed",
    "cancelling",
    "cancelled",
]

RunMode = Literal["sync", "async", "stream"]

TERMINAL_STATUSES: frozenset[str] = frozenset({"completed", "failed", "cancelled"})


def is_terminal_status(status: str) -> bool:
    return status in TERMINAL_STATUSES


@dataclass
class AgentCapability:
    name: str
    description: str | None = None


@dataclass
class AgentMetadata:
    documentation: str | None = None
    framework: str | None = None
    capabilities: list[AgentCapability] | None = None
    tags: list[str] | None = None


@dataclass
class AgentManifest:
    name: str
    description: str | None = None
    input_content_types: list[str] | None = None
    output_content_types: list[str] | None = None
    metadata: AgentMetadata | None = None


@dataclass
class MessagePart:
    content_type: str = "text/plain"
    content: str = ""
    name: str | None = None
    content_encoding: str | None = None
    content_url: str | None = None
    metadata: dict[str, Any] | None = None


@dataclass
class Message:
    role: str = ""
    parts: list[MessagePart] = field(default_factory=list)
    created_at: str | None = None
    completed_at: str | None = None


@dataclass
class AcpError:
    message: str = ""
    code: int = 0
    data: Any = None


@dataclass
class AwaitRequest:
    message: Message | None = None


@dataclass
class Run:
    agent_name: str = ""
    run_id: str = ""
    session_id: str = ""
    status: str = "created"
    output: list[Message] = field(default_factory=list)
    await_request: AwaitRequest | None = None
    error: AcpError | None = None
    created_at: str = ""
    finished_at: str | None = None


@dataclass
class Event:
    type: str = ""
    run: Run | None = None
    message: Message | None = None
    part: MessagePart | None = None
    error: AcpError | None = None
    generic: Any = None


@dataclass
class SessionData:
    id: str = ""
    title: str = ""
    summary_message_id: str | None = None
    message_count: int = 0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    cost: float = 0.0
    created_at: int = 0
    updated_at: int = 0


@dataclass
class SessionMessage:
    id: str = ""
    session_id: str = ""
    role: str = ""
    parts: str = ""
    model: str | None = None
    provider: str | None = None
    is_summary_message: bool = False
    created_at: int = 0
    updated_at: int = 0


@dataclass
class SessionSnapshot:
    version: int = 0
    session: SessionData = field(default_factory=SessionData)
    messages: list[SessionMessage] = field(default_factory=list)


def new_user_message(text: str) -> Message:
    return Message(role="user", parts=[MessagePart(content_type="text/plain", content=text)])


def new_agent_message(text: str) -> Message:
    return Message(role="agent", parts=[MessagePart(content_type="text/plain", content=text)])


def text_content(messages: list[Message]) -> str:
    parts: list[str] = []
    for msg in messages:
        for part in msg.parts:
            if part.content_type in ("text/plain", ""):
                parts.append(part.content)
    return "\n".join(parts)
