from crush_acp_sdk.types import (
    new_user_message,
    new_agent_message,
    text_content,
    is_terminal_status,
    Message,
    MessagePart,
)


def test_new_user_message():
    msg = new_user_message("hello")
    assert msg.role == "user"
    assert len(msg.parts) == 1
    assert msg.parts[0].content_type == "text/plain"
    assert msg.parts[0].content == "hello"


def test_new_agent_message():
    msg = new_agent_message("hi")
    assert msg.role == "agent"
    assert len(msg.parts) == 1
    assert msg.parts[0].content == "hi"


def test_text_content():
    messages = [
        Message(role="agent", parts=[MessagePart(content_type="text/plain", content="Hello")]),
        Message(role="agent", parts=[MessagePart(content_type="text/plain", content="World")]),
    ]
    assert text_content(messages) == "Hello\nWorld"


def test_text_content_empty():
    assert text_content([]) == ""


def test_text_content_skips_non_text():
    messages = [
        Message(
            role="agent",
            parts=[
                MessagePart(content_type="image/png", content="data"),
                MessagePart(content_type="text/plain", content="caption"),
            ],
        ),
    ]
    assert text_content(messages) == "caption"


def test_is_terminal_status():
    assert is_terminal_status("completed") is True
    assert is_terminal_status("failed") is True
    assert is_terminal_status("cancelled") is True
    assert is_terminal_status("created") is False
    assert is_terminal_status("in-progress") is False
    assert is_terminal_status("awaiting") is False
    assert is_terminal_status("cancelling") is False
