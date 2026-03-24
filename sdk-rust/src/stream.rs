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
