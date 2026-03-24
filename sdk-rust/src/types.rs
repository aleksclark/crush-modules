use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fmt;

/// Status of an ACP run.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum RunStatus {
    #[serde(rename = "created")]
    Created,
    #[serde(rename = "in-progress")]
    InProgress,
    #[serde(rename = "awaiting")]
    Awaiting,
    #[serde(rename = "completed")]
    Completed,
    #[serde(rename = "failed")]
    Failed,
    #[serde(rename = "cancelling")]
    Cancelling,
    #[serde(rename = "cancelled")]
    Cancelled,
}

impl RunStatus {
    pub fn is_terminal(&self) -> bool {
        matches!(
            self,
            RunStatus::Completed | RunStatus::Failed | RunStatus::Cancelled
        )
    }
}

impl fmt::Display for RunStatus {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            RunStatus::Created => write!(f, "created"),
            RunStatus::InProgress => write!(f, "in-progress"),
            RunStatus::Awaiting => write!(f, "awaiting"),
            RunStatus::Completed => write!(f, "completed"),
            RunStatus::Failed => write!(f, "failed"),
            RunStatus::Cancelling => write!(f, "cancelling"),
            RunStatus::Cancelled => write!(f, "cancelled"),
        }
    }
}

/// Mode for executing a run.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum RunMode {
    #[serde(rename = "sync")]
    Sync,
    #[serde(rename = "async")]
    Async,
    #[serde(rename = "stream")]
    Stream,
}

/// Agent manifest describing a remote ACP agent's capabilities.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentManifest {
    pub name: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub input_content_types: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub output_content_types: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub metadata: Option<AgentMetadata>,
}

/// Optional discovery metadata for an agent.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentMetadata {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub documentation: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub framework: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub capabilities: Option<Vec<AgentCapability>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tags: Option<Vec<String>>,
}

/// A specific agent capability.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentCapability {
    pub name: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
}

/// Core communication unit in ACP.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub role: String,
    pub parts: Vec<MessagePart>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub created_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub completed_at: Option<String>,
}

/// A single typed content unit within a message.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MessagePart {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(default)]
    pub content_type: String,
    #[serde(default)]
    pub content: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub content_encoding: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub content_url: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub metadata: Option<HashMap<String, serde_json::Value>>,
}

/// An ACP agent run.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Run {
    pub agent_name: String,
    pub run_id: String,
    #[serde(default)]
    pub session_id: String,
    pub status: RunStatus,
    #[serde(default)]
    pub output: Vec<Message>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub await_request: Option<AwaitRequest>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<AcpError>,
    pub created_at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<String>,
}

/// An agent's request for external input.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AwaitRequest {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<Message>,
}

/// Input to resume an awaiting run.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AwaitResume {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<Message>,
}

/// Error returned by the ACP server.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AcpError {
    #[serde(default)]
    pub code: i32,
    pub message: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<serde_json::Value>,
}

impl fmt::Display for AcpError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.message)
    }
}

impl std::error::Error for AcpError {}

/// Streaming event type identifiers.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum EventType {
    #[serde(rename = "run.created")]
    RunCreated,
    #[serde(rename = "run.in-progress")]
    RunInProgress,
    #[serde(rename = "run.awaiting")]
    RunAwaiting,
    #[serde(rename = "run.completed")]
    RunCompleted,
    #[serde(rename = "run.failed")]
    RunFailed,
    #[serde(rename = "run.cancelled")]
    RunCancelled,
    #[serde(rename = "message.created")]
    MessageCreated,
    #[serde(rename = "message.part")]
    MessagePart,
    #[serde(rename = "message.completed")]
    MessageCompleted,
    #[serde(rename = "session.message")]
    SessionMessage,
    #[serde(rename = "session.snapshot")]
    SessionSnapshot,
    #[serde(rename = "error")]
    Error,
    #[serde(rename = "generic")]
    Generic,
}

impl fmt::Display for EventType {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let s = serde_json::to_value(self)
            .ok()
            .and_then(|v| v.as_str().map(String::from))
            .unwrap_or_else(|| format!("{:?}", self));
        write!(f, "{}", s)
    }
}

/// A streaming event from the ACP server.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Event {
    #[serde(rename = "type")]
    pub event_type: EventType,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub run: Option<Run>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<Message>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub part: Option<crate::types::MessagePart>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<AcpError>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub generic: Option<serde_json::Value>,
}

/// Session metadata.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionData {
    pub id: String,
    #[serde(default)]
    pub title: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub summary_message_id: Option<String>,
    #[serde(default)]
    pub message_count: i64,
    #[serde(default)]
    pub prompt_tokens: i64,
    #[serde(default)]
    pub completion_tokens: i64,
    #[serde(default)]
    pub cost: f64,
    #[serde(default)]
    pub created_at: i64,
    #[serde(default)]
    pub updated_at: i64,
}

/// A single message in a session's conversation history.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionMessage {
    pub id: String,
    pub session_id: String,
    pub role: String,
    pub parts: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub provider: Option<String>,
    #[serde(default)]
    pub is_summary_message: bool,
    #[serde(default)]
    pub created_at: i64,
    #[serde(default)]
    pub updated_at: i64,
}

/// A portable representation of a complete session including all conversation
/// history.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionSnapshot {
    pub version: i32,
    pub session: SessionData,
    pub messages: Vec<SessionMessage>,
}

/// Request to create a run (internal).
#[derive(Debug, Serialize)]
pub(crate) struct RunCreateRequest {
    pub agent_name: String,
    pub input: Vec<Message>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mode: Option<RunMode>,
}

/// Response from listing agents (internal).
#[derive(Debug, Deserialize)]
pub(crate) struct AgentsListResponse {
    pub agents: Vec<AgentManifest>,
}

/// Response from importing a session (internal).
#[derive(Debug, Deserialize)]
#[allow(dead_code)]
pub(crate) struct ImportResponse {
    pub session_id: String,
    pub message_count: i32,
    pub status: String,
}

/// Creates a simple text message with the "user" role.
pub fn new_user_message(text: &str) -> Message {
    Message {
        role: "user".to_string(),
        parts: vec![MessagePart {
            content_type: "text/plain".to_string(),
            content: text.to_string(),
            name: None,
            content_encoding: None,
            content_url: None,
            metadata: None,
        }],
        created_at: None,
        completed_at: None,
    }
}

/// Creates a simple text message with the "agent" role.
pub fn new_agent_message(text: &str) -> Message {
    Message {
        role: "agent".to_string(),
        parts: vec![MessagePart {
            content_type: "text/plain".to_string(),
            content: text.to_string(),
            name: None,
            content_encoding: None,
            content_url: None,
            metadata: None,
        }],
        created_at: None,
        completed_at: None,
    }
}

/// Extracts all text/plain content from a slice of messages.
pub fn text_content(messages: &[Message]) -> String {
    let mut result = String::new();
    for msg in messages {
        for part in &msg.parts {
            if part.content_type == "text/plain" || part.content_type.is_empty() {
                if !result.is_empty() {
                    result.push('\n');
                }
                result.push_str(&part.content);
            }
        }
    }
    result
}

/// SDK error type.
#[derive(Debug, thiserror::Error)]
pub enum SdkError {
    #[error("HTTP error: {0}")]
    Http(#[from] reqwest::Error),

    #[error("ACP error (HTTP {status}): {message}")]
    Acp { status: u16, message: String },

    #[error("JSON error: {0}")]
    Json(#[from] serde_json::Error),

    #[error("{0}")]
    Other(String),
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_run_status_is_terminal() {
        assert!(RunStatus::Completed.is_terminal());
        assert!(RunStatus::Failed.is_terminal());
        assert!(RunStatus::Cancelled.is_terminal());
        assert!(!RunStatus::Created.is_terminal());
        assert!(!RunStatus::InProgress.is_terminal());
        assert!(!RunStatus::Awaiting.is_terminal());
        assert!(!RunStatus::Cancelling.is_terminal());
    }

    #[test]
    fn test_new_user_message() {
        let msg = new_user_message("hello");
        assert_eq!(msg.role, "user");
        assert_eq!(msg.parts.len(), 1);
        assert_eq!(msg.parts[0].content_type, "text/plain");
        assert_eq!(msg.parts[0].content, "hello");
    }

    #[test]
    fn test_new_agent_message() {
        let msg = new_agent_message("hi");
        assert_eq!(msg.role, "agent");
        assert_eq!(msg.parts.len(), 1);
        assert_eq!(msg.parts[0].content, "hi");
    }

    #[test]
    fn test_text_content() {
        let messages = vec![
            Message {
                role: "agent".to_string(),
                parts: vec![MessagePart {
                    content_type: "text/plain".to_string(),
                    content: "Hello".to_string(),
                    name: None,
                    content_encoding: None,
                    content_url: None,
                    metadata: None,
                }],
                created_at: None,
                completed_at: None,
            },
            Message {
                role: "agent".to_string(),
                parts: vec![MessagePart {
                    content_type: "text/plain".to_string(),
                    content: "World".to_string(),
                    name: None,
                    content_encoding: None,
                    content_url: None,
                    metadata: None,
                }],
                created_at: None,
                completed_at: None,
            },
        ];
        assert_eq!(text_content(&messages), "Hello\nWorld");
    }

    #[test]
    fn test_text_content_empty() {
        assert_eq!(text_content(&[]), "");
    }

    #[test]
    fn test_session_result_text_empty() {
        let result = super::super::SessionResult {
            run: None,
            snapshot: None,
        };
        assert_eq!(result.text(), "");
    }

    #[test]
    fn test_run_status_display() {
        assert_eq!(RunStatus::Created.to_string(), "created");
        assert_eq!(RunStatus::InProgress.to_string(), "in-progress");
        assert_eq!(RunStatus::Completed.to_string(), "completed");
    }

    #[test]
    fn test_event_type_serialization() {
        let event_type = EventType::RunCompleted;
        let json = serde_json::to_string(&event_type).unwrap();
        assert_eq!(json, "\"run.completed\"");

        let deserialized: EventType = serde_json::from_str("\"message.part\"").unwrap();
        assert_eq!(deserialized, EventType::MessagePart);
    }

    #[test]
    fn test_run_json_roundtrip() {
        let run = Run {
            agent_name: "crush".to_string(),
            run_id: "r1".to_string(),
            session_id: "ses-1".to_string(),
            status: RunStatus::Completed,
            output: vec![new_agent_message("done")],
            await_request: None,
            error: None,
            created_at: "2025-01-01T00:00:00Z".to_string(),
            finished_at: None,
        };
        let json = serde_json::to_string(&run).unwrap();
        let deserialized: Run = serde_json::from_str(&json).unwrap();
        assert_eq!(deserialized.run_id, "r1");
        assert_eq!(deserialized.status, RunStatus::Completed);
    }

    #[test]
    fn test_acp_error_display() {
        let err = AcpError {
            code: 500,
            message: "internal error".to_string(),
            data: None,
        };
        assert_eq!(err.to_string(), "internal error");
    }
}
