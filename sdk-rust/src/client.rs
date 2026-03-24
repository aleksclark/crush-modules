use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;

use futures::StreamExt;
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};

use crate::stream::{parse_stream, CONTENT_TYPE_NDJSON};
use crate::types::*;

/// High-level SDK client for the Crush ACP protocol.
pub struct Client {
    base_url: String,
    agent_name: Mutex<Option<String>>,
    http_client: reqwest::Client,
    headers: HashMap<String, String>,
}

/// Builder for configuring a [`Client`].
pub struct ClientBuilder {
    base_url: String,
    agent_name: Option<String>,
    http_client: Option<reqwest::Client>,
    headers: HashMap<String, String>,
}

impl ClientBuilder {
    pub fn new(base_url: &str) -> Self {
        Self {
            base_url: base_url.to_string(),
            agent_name: None,
            http_client: None,
            headers: HashMap::new(),
        }
    }

    pub fn agent_name(mut self, name: &str) -> Self {
        self.agent_name = Some(name.to_string());
        self
    }

    pub fn http_client(mut self, client: reqwest::Client) -> Self {
        self.http_client = Some(client);
        self
    }

    pub fn headers(mut self, headers: HashMap<String, String>) -> Self {
        self.headers = headers;
        self
    }

    pub fn header(mut self, key: &str, value: &str) -> Self {
        self.headers.insert(key.to_string(), value.to_string());
        self
    }

    pub fn build(self) -> Client {
        let http_client = self.http_client.unwrap_or_else(|| {
            reqwest::Client::builder()
                .build()
                .expect("failed to build HTTP client")
        });

        Client {
            base_url: self.base_url,
            agent_name: Mutex::new(self.agent_name),
            http_client,
            headers: self.headers,
        }
    }
}

/// Outcome of a synchronous prompt execution.
#[derive(Debug)]
pub struct SessionResult {
    pub run: Option<Run>,
    pub snapshot: Option<SessionSnapshot>,
}

impl SessionResult {
    pub fn text(&self) -> String {
        match &self.run {
            Some(run) => text_content(&run.output),
            None => String::new(),
        }
    }
}

/// Handle to a streaming run.
pub struct Stream {
    inner: std::pin::Pin<Box<dyn futures::Stream<Item = Event> + Send>>,
    err: Option<SdkError>,
    last_run: Option<Run>,
    snapshot: Option<SessionSnapshot>,
    done: bool,
}

impl std::fmt::Debug for Stream {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Stream")
            .field("done", &self.done)
            .field("last_run", &self.last_run)
            .finish()
    }
}

impl Stream {
    pub async fn next(&mut self) -> Option<Event> {
        if self.done {
            return None;
        }

        match self.inner.next().await {
            Some(event) => {
                if let Some(ref run) = event.run {
                    self.last_run = Some(run.clone());
                }
                if event.event_type == EventType::SessionSnapshot
                    && let Some(ref generic) = event.generic
                    && let Ok(snap) = serde_json::from_value::<SessionSnapshot>(generic.clone())
                    && snap.version > 0
                {
                    self.snapshot = Some(snap);
                }
                Some(event)
            }
            None => {
                self.done = true;
                None
            }
        }
    }

    pub fn err(&self) -> Option<&SdkError> {
        self.err.as_ref()
    }

    /// Consumes all events and returns the final result.
    pub async fn result(mut self) -> Result<SessionResult, SdkError> {
        while self.next().await.is_some() {}
        if let Some(err) = self.err {
            return Err(err);
        }
        Ok(SessionResult {
            run: self.last_run,
            snapshot: self.snapshot,
        })
    }
}

impl Client {
    /// Creates a new client with default settings.
    pub fn new(base_url: &str) -> Self {
        ClientBuilder::new(base_url).build()
    }

    /// Creates a builder for configuring a client.
    pub fn builder(base_url: &str) -> ClientBuilder {
        ClientBuilder::new(base_url)
    }

    /// Checks if the ACP server is reachable.
    pub async fn ping(&self) -> Result<(), SdkError> {
        let url = format!("{}/ping", self.base_url);
        let resp = self
            .http_client
            .get(&url)
            .headers(self.build_headers())
            .send()
            .await?;

        let status = resp.status();
        let body = resp.text().await?;

        if status != reqwest::StatusCode::OK || body != "pong" {
            return Err(SdkError::Other(format!(
                "ping: unexpected response (HTTP {}): {}",
                status.as_u16(),
                body
            )));
        }
        Ok(())
    }

    /// Returns agents available on the ACP server.
    pub async fn list_agents(&self) -> Result<Vec<AgentManifest>, SdkError> {
        let url = format!("{}/agents", self.base_url);
        let resp = self
            .http_client
            .get(&url)
            .headers(self.build_headers())
            .send()
            .await?;

        if resp.status() != reqwest::StatusCode::OK {
            return Err(read_error(resp).await);
        }

        let result: AgentsListResponse = resp.json().await?;
        Ok(result.agents)
    }

    /// Starts a new session by sending a prompt to the agent.
    pub async fn new_session(&self, prompt: &str) -> Result<SessionResult, SdkError> {
        self.run_sync(None, prompt).await
    }

    /// Continues an existing session with a new prompt.
    pub async fn resume(
        &self,
        session_id: &str,
        prompt: &str,
    ) -> Result<SessionResult, SdkError> {
        if session_id.is_empty() {
            return Err(SdkError::Other(
                "session ID is required for Resume".to_string(),
            ));
        }
        self.run_sync(Some(session_id), prompt).await
    }

    /// Starts a new session with streaming output.
    pub async fn new_session_stream(&self, prompt: &str) -> Result<Stream, SdkError> {
        self.run_stream(None, prompt).await
    }

    /// Continues an existing session with streaming output.
    pub async fn resume_stream(
        &self,
        session_id: &str,
        prompt: &str,
    ) -> Result<Stream, SdkError> {
        if session_id.is_empty() {
            return Err(SdkError::Other(
                "session ID is required for ResumeStream".to_string(),
            ));
        }
        self.run_stream(Some(session_id), prompt).await
    }

    /// Exports the full session snapshot for the given session ID.
    pub async fn dump(&self, session_id: &str) -> Result<SessionSnapshot, SdkError> {
        let url = format!("{}/sessions/{}/export", self.base_url, session_id);
        let resp = self
            .http_client
            .get(&url)
            .headers(self.build_headers())
            .send()
            .await?;

        if resp.status() != reqwest::StatusCode::OK {
            return Err(read_error(resp).await);
        }

        let snapshot: SessionSnapshot = resp.json().await?;
        Ok(snapshot)
    }

    /// Imports a session snapshot into the agent.
    pub async fn restore(&self, snapshot: &SessionSnapshot) -> Result<(), SdkError> {
        let url = format!("{}/sessions/import", self.base_url);
        let resp = self
            .http_client
            .post(&url)
            .headers(self.build_headers())
            .json(snapshot)
            .send()
            .await?;

        if resp.status() != reqwest::StatusCode::OK {
            return Err(read_error(resp).await);
        }

        let result: ImportResponse = resp.json().await?;
        if result.status != "imported" {
            return Err(SdkError::Other(format!(
                "unexpected import status: {}",
                result.status
            )));
        }
        Ok(())
    }

    /// Polls the server until it responds to /ping or the timeout expires.
    pub async fn wait_ready(&self, interval: Duration, timeout: Duration) -> Result<(), SdkError> {
        let start = tokio::time::Instant::now();
        let mut tick = tokio::time::interval(interval);

        loop {
            if self.ping().await.is_ok() {
                return Ok(());
            }

            if start.elapsed() >= timeout {
                return Err(SdkError::Other("server not ready: timeout".to_string()));
            }

            tick.tick().await;
        }
    }

    async fn resolve_agent(&self) -> Result<String, SdkError> {
        {
            let guard = self.agent_name.lock().unwrap();
            if let Some(ref name) = *guard {
                return Ok(name.clone());
            }
        }

        let agents = self.list_agents().await?;
        if agents.is_empty() {
            return Err(SdkError::Other(format!(
                "no agents available on {}",
                self.base_url
            )));
        }

        let name = agents[0].name.clone();
        {
            let mut guard = self.agent_name.lock().unwrap();
            *guard = Some(name.clone());
        }
        Ok(name)
    }

    async fn run_sync(
        &self,
        session_id: Option<&str>,
        prompt: &str,
    ) -> Result<SessionResult, SdkError> {
        let agent = self.resolve_agent().await?;

        let body = RunCreateRequest {
            agent_name: agent,
            input: vec![new_user_message(prompt)],
            session_id: session_id.map(|s| s.to_string()),
            mode: Some(RunMode::Sync),
        };

        let url = format!("{}/runs", self.base_url);
        let resp = self
            .http_client
            .post(&url)
            .headers(self.build_headers())
            .json(&body)
            .send()
            .await?;

        let status = resp.status();
        if status != reqwest::StatusCode::OK && status != reqwest::StatusCode::ACCEPTED {
            return Err(read_error(resp).await);
        }

        let run: Run = resp.json().await?;
        Ok(SessionResult {
            run: Some(run),
            snapshot: None,
        })
    }

    async fn run_stream(
        &self,
        session_id: Option<&str>,
        prompt: &str,
    ) -> Result<Stream, SdkError> {
        let agent = self.resolve_agent().await?;

        let body = RunCreateRequest {
            agent_name: agent,
            input: vec![new_user_message(prompt)],
            session_id: session_id.map(|s| s.to_string()),
            mode: Some(RunMode::Stream),
        };

        let url = format!("{}/runs", self.base_url);
        let mut headers = self.build_headers();
        headers.insert(
            reqwest::header::ACCEPT,
            HeaderValue::from_static(CONTENT_TYPE_NDJSON),
        );

        let resp = self
            .http_client
            .post(&url)
            .headers(headers)
            .json(&body)
            .send()
            .await?;

        if resp.status() != reqwest::StatusCode::OK {
            return Err(read_error(resp).await);
        }

        let event_stream = parse_stream(resp);

        Ok(Stream {
            inner: Box::pin(event_stream),
            err: None,
            last_run: None,
            snapshot: None,
            done: false,
        })
    }

    fn build_headers(&self) -> HeaderMap {
        let mut map = HeaderMap::new();
        for (k, v) in &self.headers {
            if let (Ok(name), Ok(value)) = (
                HeaderName::from_bytes(k.as_bytes()),
                HeaderValue::from_str(v),
            ) {
                map.insert(name, value);
            }
        }
        map
    }
}

async fn read_error(resp: reqwest::Response) -> SdkError {
    let status = resp.status().as_u16();
    let body = resp.text().await.unwrap_or_default();

    if let Ok(acp_err) = serde_json::from_str::<AcpError>(&body)
        && !acp_err.message.is_empty()
    {
        return SdkError::Acp {
            status,
            message: acp_err.message,
        };
    }

    SdkError::Acp {
        status,
        message: body,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use wiremock::matchers::{method, path};
    use wiremock::{Mock, MockServer, ResponseTemplate};

    #[tokio::test]
    async fn test_ping() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/ping"))
            .respond_with(ResponseTemplate::new(200).set_body_string("pong"))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        client.ping().await.unwrap();
    }

    #[tokio::test]
    async fn test_ping_error() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/ping"))
            .respond_with(ResponseTemplate::new(503).set_body_string("not ready"))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let err = client.ping().await.unwrap_err();
        assert!(err.to_string().contains("unexpected response"));
    }

    #[tokio::test]
    async fn test_list_agents() {
        let mock_server = MockServer::start().await;

        let agents_resp = serde_json::json!({
            "agents": [{"name": "crush", "description": "Crush AI assistant"}]
        });

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(&agents_resp))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let agents = client.list_agents().await.unwrap();
        assert_eq!(agents.len(), 1);
        assert_eq!(agents[0].name, "crush");
    }

    #[tokio::test]
    async fn test_new_session() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        let run_resp = serde_json::json!({
            "agent_name": "crush",
            "run_id": "run-1",
            "session_id": "ses-abc",
            "status": "completed",
            "output": [{"role": "agent", "parts": [{"content_type": "text/plain", "content": "Hi there!"}]}],
            "created_at": "2025-01-01T00:00:00Z"
        });

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(ResponseTemplate::new(200).set_body_json(&run_resp))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let result = client.new_session("hello").await.unwrap();
        assert_eq!(
            result.run.as_ref().unwrap().session_id,
            "ses-abc"
        );
        assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Completed);
        assert_eq!(result.text(), "Hi there!");
    }

    #[tokio::test]
    async fn test_resume() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agent_name": "crush",
                "run_id": "run-2",
                "session_id": "ses-abc",
                "status": "completed",
                "output": [{"role": "agent", "parts": [{"content_type": "text/plain", "content": "Fixed the bug."}]}],
                "created_at": "2025-01-01T00:00:00Z"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let result = client.resume("ses-abc", "fix it").await.unwrap();
        assert_eq!(result.run.as_ref().unwrap().session_id, "ses-abc");
        assert_eq!(result.text(), "Fixed the bug.");
    }

    #[tokio::test]
    async fn test_resume_requires_session_id() {
        let client = Client::new("http://localhost:9999");
        let err = client.resume("", "hello").await.unwrap_err();
        assert!(err.to_string().contains("session ID is required"));
    }

    #[tokio::test]
    async fn test_new_session_stream() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        let events = vec![
            r#"{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}"#,
            r#"{"type":"run.in-progress","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"in-progress","output":[],"created_at":"2025-01-01T00:00:00Z"}}"#,
            r#"{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}"#,
            r#"{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}"#,
            r#"{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"Hello World"}]}],"created_at":"2025-01-01T00:00:00Z"}}"#,
        ];
        let body = events.join("\n") + "\n";

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(
                ResponseTemplate::new(200)
                    .insert_header("content-type", CONTENT_TYPE_NDJSON)
                    .set_body_string(body),
            )
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let mut stream = client.new_session_stream("hi").await.unwrap();

        let mut parts = Vec::new();
        let mut event_types = Vec::new();
        while let Some(ev) = stream.next().await {
            event_types.push(ev.event_type.clone());
            if ev.event_type == EventType::MessagePart {
                if let Some(ref part) = ev.part {
                    parts.push(part.content.clone());
                }
            }
        }

        assert!(stream.err().is_none());
        assert_eq!(parts, vec!["Hello", " World"]);
        assert!(event_types.contains(&EventType::RunCreated));
        assert!(event_types.contains(&EventType::RunCompleted));
    }

    #[tokio::test]
    async fn test_stream_result() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        let events = vec![
            r#"{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}"#,
            r#"{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"done"}]}],"created_at":"2025-01-01T00:00:00Z"}}"#,
        ];
        let body = events.join("\n") + "\n";

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(
                ResponseTemplate::new(200)
                    .insert_header("content-type", CONTENT_TYPE_NDJSON)
                    .set_body_string(body),
            )
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let stream = client.new_session_stream("hi").await.unwrap();

        let result = stream.result().await.unwrap();
        assert!(result.run.is_some());
        assert_eq!(result.run.as_ref().unwrap().session_id, "ses-1");
        assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Completed);
    }

    #[tokio::test]
    async fn test_resume_stream_requires_session_id() {
        let client = Client::new("http://localhost:9999");
        let err = client.resume_stream("", "hello").await.unwrap_err();
        assert!(err.to_string().contains("session ID is required"));
    }

    #[tokio::test]
    async fn test_dump() {
        let mock_server = MockServer::start().await;

        let snapshot = serde_json::json!({
            "version": 1,
            "session": {
                "id": "ses-abc",
                "title": "Fix auth bug",
                "message_count": 4,
                "prompt_tokens": 0,
                "completion_tokens": 0,
                "cost": 0.0,
                "created_at": 1700000000,
                "updated_at": 1700000120
            },
            "messages": [
                {
                    "id": "msg-1",
                    "session_id": "ses-abc",
                    "role": "user",
                    "parts": "[{\"type\":\"text\",\"data\":{\"text\":\"Fix the bug\"}}]",
                    "created_at": 1700000000,
                    "updated_at": 1700000000
                },
                {
                    "id": "msg-2",
                    "session_id": "ses-abc",
                    "role": "assistant",
                    "parts": "[{\"type\":\"text\",\"data\":{\"text\":\"Done.\"}}]",
                    "model": "claude-opus-4",
                    "provider": "bedrock",
                    "created_at": 1700000010,
                    "updated_at": 1700000015
                }
            ]
        });

        Mock::given(method("GET"))
            .and(path("/sessions/ses-abc/export"))
            .respond_with(ResponseTemplate::new(200).set_body_json(&snapshot))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let result = client.dump("ses-abc").await.unwrap();
        assert_eq!(result.version, 1);
        assert_eq!(result.session.id, "ses-abc");
        assert_eq!(result.session.title, "Fix auth bug");
        assert_eq!(result.messages.len(), 2);
        assert_eq!(result.messages[0].role, "user");
        assert_eq!(result.messages[1].role, "assistant");
        assert_eq!(
            result.messages[1].model.as_deref(),
            Some("claude-opus-4")
        );
    }

    #[tokio::test]
    async fn test_dump_not_found() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/sessions/ses-xxx/export"))
            .respond_with(ResponseTemplate::new(404).set_body_json(serde_json::json!({
                "code": 404,
                "message": "session \"ses-xxx\" not found"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let err = client.dump("ses-xxx").await.unwrap_err();
        assert!(err.to_string().contains("not found"));
    }

    #[tokio::test]
    async fn test_restore() {
        let mock_server = MockServer::start().await;

        Mock::given(method("POST"))
            .and(path("/sessions/import"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "session_id": "ses-abc",
                "message_count": 2,
                "status": "imported"
            })))
            .mount(&mock_server)
            .await;

        let snapshot = SessionSnapshot {
            version: 1,
            session: SessionData {
                id: "ses-abc".to_string(),
                title: "Fix auth bug".to_string(),
                summary_message_id: None,
                message_count: 2,
                prompt_tokens: 0,
                completion_tokens: 0,
                cost: 0.0,
                created_at: 0,
                updated_at: 0,
            },
            messages: vec![
                SessionMessage {
                    id: "msg-1".to_string(),
                    session_id: "ses-abc".to_string(),
                    role: "user".to_string(),
                    parts: r#"[{"type":"text","data":{"text":"hello"}}]"#.to_string(),
                    model: None,
                    provider: None,
                    is_summary_message: false,
                    created_at: 0,
                    updated_at: 0,
                },
                SessionMessage {
                    id: "msg-2".to_string(),
                    session_id: "ses-abc".to_string(),
                    role: "assistant".to_string(),
                    parts: r#"[{"type":"text","data":{"text":"hi"}}]"#.to_string(),
                    model: None,
                    provider: None,
                    is_summary_message: false,
                    created_at: 0,
                    updated_at: 0,
                },
            ],
        };

        let client = Client::new(&mock_server.uri());
        client.restore(&snapshot).await.unwrap();
    }

    #[tokio::test]
    async fn test_restore_error() {
        let mock_server = MockServer::start().await;

        Mock::given(method("POST"))
            .and(path("/sessions/import"))
            .respond_with(ResponseTemplate::new(400).set_body_json(serde_json::json!({
                "code": 400,
                "message": "snapshot version is required"
            })))
            .mount(&mock_server)
            .await;

        let snapshot = SessionSnapshot {
            version: 0,
            session: SessionData {
                id: String::new(),
                title: String::new(),
                summary_message_id: None,
                message_count: 0,
                prompt_tokens: 0,
                completion_tokens: 0,
                cost: 0.0,
                created_at: 0,
                updated_at: 0,
            },
            messages: vec![],
        };

        let client = Client::new(&mock_server.uri());
        let err = client.restore(&snapshot).await.unwrap_err();
        assert!(err.to_string().contains("snapshot version is required"));
    }

    #[tokio::test]
    async fn test_full_round_trip() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agent_name": "crush",
                "run_id": "run-1",
                "session_id": "ses-roundtrip",
                "status": "completed",
                "output": [{"role": "agent", "parts": [{"content_type": "text/plain", "content": "First response"}]}],
                "created_at": "2025-01-01T00:00:00Z"
            })))
            .mount(&mock_server)
            .await;

        Mock::given(method("GET"))
            .and(path("/sessions/ses-roundtrip/export"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "version": 1,
                "session": {
                    "id": "ses-roundtrip",
                    "title": "Round trip test",
                    "message_count": 2,
                    "prompt_tokens": 0,
                    "completion_tokens": 0,
                    "cost": 0.0,
                    "created_at": 0,
                    "updated_at": 0
                },
                "messages": [
                    {"id": "m1", "session_id": "ses-roundtrip", "role": "user", "parts": "[]", "created_at": 0, "updated_at": 0},
                    {"id": "m2", "session_id": "ses-roundtrip", "role": "assistant", "parts": "[]", "created_at": 0, "updated_at": 0}
                ]
            })))
            .mount(&mock_server)
            .await;

        Mock::given(method("POST"))
            .and(path("/sessions/import"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "session_id": "ses-roundtrip",
                "message_count": 2,
                "status": "imported"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());

        let result = client.new_session("hello").await.unwrap();
        assert_eq!(result.run.as_ref().unwrap().session_id, "ses-roundtrip");
        assert_eq!(result.text(), "First response");

        let snapshot = client.dump("ses-roundtrip").await.unwrap();
        assert_eq!(snapshot.session.id, "ses-roundtrip");
        assert_eq!(snapshot.messages.len(), 2);

        client.restore(&snapshot).await.unwrap();

        let resumed = client
            .resume("ses-roundtrip", "continue")
            .await
            .unwrap();
        assert_eq!(resumed.text(), "First response");
    }

    #[tokio::test]
    async fn test_wait_ready() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/ping"))
            .respond_with(ResponseTemplate::new(200).set_body_string("pong"))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        client
            .wait_ready(Duration::from_millis(50), Duration::from_secs(2))
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn test_wait_ready_timeout() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/ping"))
            .respond_with(ResponseTemplate::new(503))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let err = client
            .wait_ready(Duration::from_millis(50), Duration::from_millis(200))
            .await
            .unwrap_err();
        assert!(err.to_string().contains("not ready"));
    }

    #[tokio::test]
    async fn test_with_headers() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/ping"))
            .and(wiremock::matchers::header("Authorization", "Bearer my-token"))
            .respond_with(ResponseTemplate::new(200).set_body_string("pong"))
            .mount(&mock_server)
            .await;

        let client = Client::builder(&mock_server.uri())
            .header("Authorization", "Bearer my-token")
            .build();
        client.ping().await.unwrap();
    }

    #[tokio::test]
    async fn test_with_agent_name() {
        let mock_server = MockServer::start().await;

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agent_name": "my-agent",
                "run_id": "r1",
                "session_id": "",
                "status": "completed",
                "output": [{"role": "agent", "parts": [{"content_type": "text/plain", "content": "ok"}]}],
                "created_at": "2025-01-01T00:00:00Z"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::builder(&mock_server.uri())
            .agent_name("my-agent")
            .build();
        let result = client.new_session("hi").await.unwrap();
        assert_eq!(result.text(), "ok");
    }

    #[tokio::test]
    async fn test_auto_detect_agent() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "auto-crush"}]
            })))
            .expect(1)
            .mount(&mock_server)
            .await;

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agent_name": "auto-crush",
                "run_id": "r1",
                "session_id": "",
                "status": "completed",
                "output": [{"role": "agent", "parts": [{"content_type": "text/plain", "content": "ok"}]}],
                "created_at": "2025-01-01T00:00:00Z"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        client.new_session("hi").await.unwrap();
    }

    #[tokio::test]
    async fn test_server_error() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(500).set_body_json(serde_json::json!({
                "code": 500,
                "message": "internal error"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let err = client.list_agents().await.unwrap_err();
        assert!(err.to_string().contains("internal error"));
    }

    #[tokio::test]
    async fn test_run_failed() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agent_name": "crush",
                "run_id": "r1",
                "session_id": "",
                "status": "failed",
                "error": {"code": 500, "message": "agent crashed"},
                "output": [],
                "created_at": "2025-01-01T00:00:00Z"
            })))
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let result = client.new_session("hi").await.unwrap();
        assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Failed);
        assert!(result.run.as_ref().unwrap().error.is_some());
    }

    #[tokio::test]
    async fn test_session_snapshot_in_stream() {
        let mock_server = MockServer::start().await;

        Mock::given(method("GET"))
            .and(path("/agents"))
            .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
                "agents": [{"name": "crush"}]
            })))
            .mount(&mock_server)
            .await;

        let snapshot = serde_json::json!({
            "version": 1,
            "session": {"id": "ses-snap", "title": "Snapshot test", "message_count": 2, "prompt_tokens": 0, "completion_tokens": 0, "cost": 0.0, "created_at": 0, "updated_at": 0},
            "messages": [{"id": "m1", "session_id": "ses-snap", "role": "user", "parts": "[]", "created_at": 0, "updated_at": 0}]
        });
        let snap_json = serde_json::to_string(&snapshot).unwrap();

        let events = vec![
            r#"{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-snap","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}"#.to_string(),
            format!(r#"{{"type":"session.snapshot","generic":{}}}"#, snap_json),
            r#"{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-snap","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"ok"}]}],"created_at":"2025-01-01T00:00:00Z"}}"#.to_string(),
        ];
        let body = events.join("\n") + "\n";

        Mock::given(method("POST"))
            .and(path("/runs"))
            .respond_with(
                ResponseTemplate::new(200)
                    .insert_header("content-type", CONTENT_TYPE_NDJSON)
                    .set_body_string(body),
            )
            .mount(&mock_server)
            .await;

        let client = Client::new(&mock_server.uri());
        let stream = client.new_session_stream("hi").await.unwrap();
        let result = stream.result().await.unwrap();

        assert!(result.snapshot.is_some());
        let snap = result.snapshot.unwrap();
        assert_eq!(snap.session.id, "ses-snap");
        assert_eq!(snap.messages.len(), 1);
    }
}
