use futures::StreamExt;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio_stream::wrappers::LinesStream;

use crate::types::{AcpError, Event, EventType};

pub(crate) const CONTENT_TYPE_NDJSON: &str = "application/x-ndjson";

/// Parses an NDJSON byte stream into typed events.
pub(crate) fn parse_stream(
    body: reqwest::Response,
) -> impl futures::Stream<Item = Event> + Send + Unpin {
    let byte_stream = body.bytes_stream();
    let reader = tokio_util::io::StreamReader::new(
        byte_stream.map(|r| r.map_err(std::io::Error::other)),
    );
    let buf_reader = BufReader::new(reader);
    let lines = LinesStream::new(buf_reader.lines());

    Box::pin(lines.filter_map(|line_result: Result<String, std::io::Error>| async move {
        match line_result {
            Ok(line) => {
                if line.is_empty() {
                    return None;
                }
                if looks_like_sse(&line) {
                    let preview = if line.len() > 40 { &line[..40] } else { &line };
                    return Some(Event {
                        event_type: EventType::Error,
                        run: None,
                        message: None,
                        part: None,
                        error: Some(AcpError {
                            code: 0,
                            message: format!(
                                "server sent SSE-formatted data instead of NDJSON \
                                 (got line starting with {:?}) \u{2014} the ACP server must use \
                                 streamable HTTP (application/x-ndjson), not SSE \
                                 (text/event-stream)",
                                preview,
                            ),
                            data: None,
                        }),
                        generic: None,
                    });
                }
                match serde_json::from_str::<Event>(&line) {
                    Ok(event) => Some(event),
                    Err(e) => Some(Event {
                        event_type: EventType::Error,
                        run: None,
                        message: None,
                        part: None,
                        error: Some(AcpError {
                            code: 0,
                            message: format!("failed to parse event: {}", e),
                            data: None,
                        }),
                        generic: None,
                    }),
                }
            }
            Err(e) => Some(Event {
                event_type: EventType::Error,
                run: None,
                message: None,
                part: None,
                error: Some(AcpError {
                    code: 0,
                    message: format!("stream read error: {}", e),
                    data: None,
                }),
                generic: None,
            }),
        }
    }))
        as std::pin::Pin<Box<dyn futures::Stream<Item = Event> + Send>>
}

/// Returns true if the line looks like an SSE field rather than raw NDJSON.
fn looks_like_sse(line: &str) -> bool {
    line.starts_with("data:")
        || line.starts_with("event:")
        || line.starts_with("id:")
        || line.starts_with("retry:")
        || line.starts_with(':')
        || line == "[DONE]"
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_looks_like_sse_plain_json() {
        assert!(!looks_like_sse(r#"{"type":"error"}"#));
    }

    #[test]
    fn test_looks_like_sse_data_prefix() {
        assert!(looks_like_sse(r#"data: {"type":"error"}"#));
        assert!(looks_like_sse(r#"data:{"type":"error"}"#));
    }

    #[test]
    fn test_looks_like_sse_event_line() {
        assert!(looks_like_sse("event: message"));
    }

    #[test]
    fn test_looks_like_sse_id_line() {
        assert!(looks_like_sse("id: 123"));
    }

    #[test]
    fn test_looks_like_sse_retry_line() {
        assert!(looks_like_sse("retry: 3000"));
    }

    #[test]
    fn test_looks_like_sse_comment() {
        assert!(looks_like_sse(": heartbeat"));
        assert!(looks_like_sse(":"));
    }

    #[test]
    fn test_looks_like_sse_done() {
        assert!(looks_like_sse("[DONE]"));
    }

    #[test]
    fn test_looks_like_sse_garbage() {
        assert!(!looks_like_sse("not-json-at-all"));
    }
}
