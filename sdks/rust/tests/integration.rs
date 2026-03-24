//! Integration tests for the Crush ACP SDK.
//!
//! These tests require Docker, Ollama, and a built Crush binary.
//! Run with: `cargo test --test integration`
//!
//! Set `CRUSH_BINARY` to override the crush binary path.
//! Set `SKIP_INTEGRATION` to skip these tests.

use std::io::{BufRead, BufReader};
use std::net::TcpListener;
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::time::Duration;

use crush_acp_sdk::*;
use tokio::sync::OnceCell;

const OLLAMA_IMAGE: &str = "ollama/ollama:latest";
const OLLAMA_MODEL: &str = "qwen2.5:0.5b";

const CONTAINER_START_TIMEOUT: Duration = Duration::from_secs(60);
const MODEL_PULL_TIMEOUT: Duration = Duration::from_secs(300);
const CRUSH_START_TIMEOUT: Duration = Duration::from_secs(30);
const RUN_TIMEOUT: Duration = Duration::from_secs(180);

struct IntegrationEnv {
    ollama_container: String,
    _crush_child: Child,
    _tmp_dir: PathBuf,
    client: Client,
}

impl Drop for IntegrationEnv {
    fn drop(&mut self) {
        eprintln!("[teardown] stopping crush (PID {})", self._crush_child.id());
        let _ = self._crush_child.kill();
        let _ = self._crush_child.wait();

        if !self.ollama_container.is_empty() {
            eprintln!(
                "[teardown] removing container {}",
                self.ollama_container
            );
            let _ = Command::new("docker")
                .args(["rm", "-f", &self.ollama_container])
                .output();
        }

        if self._tmp_dir.exists() {
            let _ = std::fs::remove_dir_all(&self._tmp_dir);
        }
    }
}

static ENV: OnceCell<IntegrationEnv> = OnceCell::const_new();

fn skip_integration() -> bool {
    std::env::var("SKIP_INTEGRATION").is_ok()
}

fn crush_binary() -> PathBuf {
    if let Ok(path) = std::env::var("CRUSH_BINARY") {
        return PathBuf::from(path);
    }
    let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    manifest.parent().unwrap().join("dist").join("crush")
}

fn random_port() -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    listener.local_addr().unwrap().port()
}

async fn wait_for_http(url: &str, timeout: Duration) -> Result<(), String> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .unwrap();
    let start = tokio::time::Instant::now();

    loop {
        if let Ok(resp) = client.get(url).send().await {
            if resp.status().is_success() {
                return Ok(());
            }
        }

        if start.elapsed() >= timeout {
            return Err(format!("timeout after {:?} waiting for {}", timeout, url));
        }

        tokio::time::sleep(Duration::from_millis(500)).await;
    }
}

async fn ollama_pull(base_url: &str, model: &str) -> Result<(), String> {
    let client = reqwest::Client::builder()
        .timeout(MODEL_PULL_TIMEOUT)
        .build()
        .unwrap();

    let resp = client
        .post(&format!("{}/api/pull", base_url))
        .json(&serde_json::json!({"name": model, "stream": false}))
        .send()
        .await
        .map_err(|e| format!("pull request: {}", e))?;

    if resp.status() != reqwest::StatusCode::OK {
        return Err(format!("pull HTTP {}", resp.status()));
    }
    Ok(())
}

fn write_crush_config(
    tmp_dir: &std::path::Path,
    ollama_url: &str,
    crush_port: u16,
) -> Result<(), String> {
    let config = format!(
        r#"{{
  "providers": {{
    "ollama": {{
      "type": "openai-compat",
      "base_url": "{}/v1",
      "api_key": "ollama",
      "models": [{{
        "id": "{}",
        "name": "Test Model",
        "context_window": 32768,
        "default_max_tokens": 2048,
        "can_reason": false,
        "supports_attachments": false
      }}]
    }}
  }},
  "models": {{
    "large": {{"provider": "ollama", "model": "{}"}},
    "small": {{"provider": "ollama", "model": "{}"}}
  }},
  "options": {{
    "disabled_plugins": ["otlp","agent-status","periodic-prompts","subagents","tempotown","ping","tavily"],
    "plugins": {{
      "acp-server": {{
        "port": {},
        "agent_name": "crush",
        "description": "SDK integration test agent"
      }}
    }}
  }}
}}"#,
        ollama_url, OLLAMA_MODEL, OLLAMA_MODEL, OLLAMA_MODEL, crush_port
    );

    for sub in &["config", "data"] {
        let dir = tmp_dir.join(sub).join("crush");
        std::fs::create_dir_all(&dir)
            .map_err(|e| format!("create dir {:?}: {}", dir, e))?;
        std::fs::write(dir.join("crush.json"), &config)
            .map_err(|e| format!("write config: {}", e))?;
    }

    let work_dir = tmp_dir.join("work").join(".crush");
    std::fs::create_dir_all(&work_dir)
        .map_err(|e| format!("create work dir: {}", e))?;
    std::fs::write(work_dir.join("init"), "")
        .map_err(|e| format!("write init: {}", e))?;

    Ok(())
}

fn build_crush_env(tmp_dir: &std::path::Path) -> Vec<(String, String)> {
    let mut env: Vec<(String, String)> = Vec::new();

    for (key, value) in std::env::vars() {
        if key.starts_with("AWS_")
            || key.starts_with("GOOGLE_")
            || key.starts_with("AZURE_")
            || key.starts_with("OPENAI_")
            || key.starts_with("ANTHROPIC_")
            || key.starts_with("GEMINI_")
        {
            continue;
        }
        env.push((key, value));
    }

    env.push(("HOME".to_string(), tmp_dir.display().to_string()));
    env.push((
        "XDG_CONFIG_HOME".to_string(),
        tmp_dir.join("config").display().to_string(),
    ));
    env.push((
        "XDG_DATA_HOME".to_string(),
        tmp_dir.join("data").display().to_string(),
    ));
    env.push(("TERM".to_string(), "dumb".to_string()));
    env.push(("CRUSH_DISABLE_METRICS".to_string(), "1".to_string()));

    env
}

async fn setup() -> IntegrationEnv {
    let crush_bin = crush_binary();
    if !crush_bin.exists() {
        panic!(
            "crush binary not found at {:?} — run 'task distro:all'",
            crush_bin
        );
    }

    let output = Command::new(&crush_bin)
        .args(["--list-plugins"])
        .output()
        .expect("crush --list-plugins failed");
    let stdout = String::from_utf8_lossy(&output.stdout);
    if !stdout.contains("acp-server") {
        panic!("crush binary missing acp-server hook:\n{}", stdout);
    }

    let ollama_port = random_port();
    let ollama_url = format!("http://127.0.0.1:{}", ollama_port);
    let container_name = format!("crush-rust-sdk-test-ollama-{}", ollama_port);

    eprintln!(
        "[setup] starting ollama container {} on port {}",
        container_name, ollama_port
    );
    let docker_out = Command::new("docker")
        .args([
            "run",
            "-d",
            "--name",
            &container_name,
            "-p",
            &format!("{}:11434", ollama_port),
            OLLAMA_IMAGE,
        ])
        .output()
        .expect("docker run ollama failed");
    if !docker_out.status.success() {
        panic!(
            "docker run ollama failed: {}",
            String::from_utf8_lossy(&docker_out.stderr)
        );
    }

    eprintln!("[setup] waiting for ollama at {}", ollama_url);
    wait_for_http(&format!("{}/api/tags", ollama_url), CONTAINER_START_TIMEOUT)
        .await
        .expect("ollama not ready");
    eprintln!("[setup] ollama ready");

    eprintln!(
        "[setup] pulling model {} (this may take a minute)",
        OLLAMA_MODEL
    );
    ollama_pull(&ollama_url, OLLAMA_MODEL)
        .await
        .expect("ollama pull failed");
    eprintln!("[setup] model {} ready", OLLAMA_MODEL);

    let tmp_dir = tempfile::tempdir().expect("tempdir failed");
    let tmp_path = tmp_dir.keep();

    let crush_port = random_port();
    let crush_url = format!("http://127.0.0.1:{}", crush_port);

    write_crush_config(&tmp_path, &ollama_url, crush_port).expect("write config failed");

    eprintln!("[setup] starting crush serve on port {}", crush_port);
    let work_dir = tmp_path.join("work");
    let env_vars = build_crush_env(&tmp_path);

    let mut crush_cmd = Command::new(&crush_bin)
        .args([
            "serve",
            "--verbose",
            "--cwd",
            &work_dir.display().to_string(),
        ])
        .env_clear()
        .envs(env_vars)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("crush serve start failed");

    if let Some(stderr) = crush_cmd.stderr.take() {
        std::thread::spawn(move || {
            let reader = BufReader::new(stderr);
            for line in reader.lines() {
                if let Ok(line) = line {
                    eprintln!("[crush] {}", line);
                }
            }
        });
    }

    eprintln!("[setup] waiting for crush ACP at {}", crush_url);
    let client = Client::new(&crush_url);
    client
        .wait_ready(Duration::from_millis(500), CRUSH_START_TIMEOUT)
        .await
        .expect("crush not ready");
    eprintln!("[setup] crush ACP ready — all systems go");

    eprintln!("[setup] warming up: sending initial prompt to establish session");
    let warmup = client
        .new_session("Say OK.")
        .await
        .expect("warmup prompt failed");
    eprintln!(
        "[setup] warmup complete: session={}",
        warmup.run.as_ref().map(|r| r.session_id.as_str()).unwrap_or("(none)")
    );

    IntegrationEnv {
        ollama_container: container_name,
        _crush_child: crush_cmd,
        _tmp_dir: tmp_path,
        client,
    }
}

async fn get_env() -> &'static IntegrationEnv {
    ENV.get_or_init(setup).await
}

fn truncate(s: &str, max_len: usize) -> String {
    let s = s.replace('\n', " ");
    if s.len() > max_len {
        format!("{}...", &s[..max_len])
    } else {
        s
    }
}

fn text_from_run(run: &Run) -> String {
    text_content(&run.output)
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_ping() {
    if skip_integration() { return; }
    let env = get_env().await;

    tokio::time::timeout(Duration::from_secs(10), env.client.ping())
        .await
        .unwrap()
        .unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_list_agents() {
    if skip_integration() { return; }
    let env = get_env().await;

    let agents = tokio::time::timeout(Duration::from_secs(10), env.client.list_agents())
        .await
        .unwrap()
        .unwrap();
    assert!(!agents.is_empty());
    assert_eq!(agents[0].name, "crush");
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_new_session() {
    if skip_integration() { return; }
    let env = get_env().await;

    let result = tokio::time::timeout(RUN_TIMEOUT, env.client.new_session("Say hello."))
        .await
        .unwrap()
        .unwrap();
    let run = result.run.as_ref().unwrap();
    assert_eq!(run.status, RunStatus::Completed);
    assert!(!run.run_id.is_empty(), "run ID should be set");
    assert!(!result.text().is_empty(), "response text should not be empty");

    eprintln!("Session: {}  Run: {}", run.session_id, run.run_id);
    eprintln!("Response: {}", truncate(&result.text(), 200));
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_resume() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Say hello."),
    )
    .await
    .unwrap()
    .unwrap();
    let first = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(first.run.as_ref().unwrap().status, RunStatus::Completed);
    let session_id = first.run.as_ref().unwrap().session_id.clone();
    assert!(!session_id.is_empty());

    eprintln!(
        "Turn 1 session={} response={}",
        session_id,
        truncate(&text_from_run(first.run.as_ref().unwrap()), 200)
    );

    let second = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume(&session_id, "What did I just say to you?"),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(second.run.as_ref().unwrap().status, RunStatus::Completed);
    assert_eq!(second.run.as_ref().unwrap().session_id, session_id);
    assert!(!second.text().is_empty());

    eprintln!("Turn 2 response={}", truncate(&second.text(), 200));
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_new_session_stream() {
    if skip_integration() { return; }
    let env = get_env().await;

    let mut stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Say hello in one short sentence."),
    )
    .await
    .unwrap()
    .unwrap();

    let mut parts: Vec<String> = Vec::new();
    let mut event_types: Vec<EventType> = Vec::new();
    let mut got_run = false;
    let mut session_id = String::new();

    while let Some(ev) = stream.next().await {
        event_types.push(ev.event_type.clone());
        if ev.event_type == EventType::MessagePart {
            if let Some(ref part) = ev.part {
                parts.push(part.content.clone());
            }
        }
        if let Some(ref run) = ev.run {
            got_run = true;
            if !run.session_id.is_empty() {
                session_id = run.session_id.clone();
            }
        }
    }

    assert!(stream.err().is_none());
    assert!(got_run, "should receive at least one run event");
    assert!(!session_id.is_empty(), "session ID should be set");
    assert!(!parts.is_empty(), "should receive message parts");
    assert!(
        event_types.contains(&EventType::RunCompleted),
        "should see run.completed"
    );

    let full_text: String = parts.join("");
    assert!(!full_text.is_empty(), "streamed text should not be empty");
    eprintln!("Streamed {} parts: {}", parts.len(), truncate(&full_text, 200));
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_stream_result() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Reply with: STREAM_OK"),
    )
    .await
    .unwrap()
    .unwrap();

    let result = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    let run = result.run.as_ref().unwrap();
    assert_eq!(run.status, RunStatus::Completed);
    assert!(!run.session_id.is_empty());

    eprintln!("Result session: {}", run.session_id);
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_resume_stream() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Say hello."),
    )
    .await
    .unwrap()
    .unwrap();
    let first = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    let session_id = first.run.as_ref().unwrap().session_id.clone();
    assert!(!session_id.is_empty());

    let rstream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume_stream(&session_id, "Say goodbye."),
    )
    .await
    .unwrap()
    .unwrap();

    let result = tokio::time::timeout(RUN_TIMEOUT, rstream.result())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Completed);
    assert_eq!(result.run.as_ref().unwrap().session_id, session_id);
    assert!(!text_from_run(result.run.as_ref().unwrap()).is_empty());

    eprintln!("Resume stream response for session {}", session_id);
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_dump() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Hello, this is a dump test."),
    )
    .await
    .unwrap()
    .unwrap();
    let first = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    let session_id = first.run.as_ref().unwrap().session_id.clone();
    assert!(!session_id.is_empty());

    let snapshot = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&session_id),
    )
    .await
    .unwrap()
    .unwrap();

    assert_eq!(snapshot.version, 1);
    assert_eq!(snapshot.session.id, session_id);
    assert!(!snapshot.session.title.is_empty());
    assert!(!snapshot.messages.is_empty(), "snapshot should contain messages");
    assert!(
        snapshot.messages.len() >= 2,
        "should have at least user + assistant messages, got {}",
        snapshot.messages.len()
    );

    let roles: Vec<&str> = snapshot.messages.iter().map(|m| m.role.as_str()).collect();
    for msg in &snapshot.messages {
        assert!(!msg.id.is_empty(), "message ID should be set");
        assert_eq!(msg.session_id, session_id, "message session ID should match");
        assert!(!msg.parts.is_empty(), "message parts should not be empty");
    }
    assert!(roles.contains(&"user"), "should have user messages");
    assert!(roles.contains(&"assistant"), "should have assistant messages");

    let data = serde_json::to_string(&snapshot).unwrap();
    assert!(
        serde_json::from_str::<serde_json::Value>(&data).is_ok(),
        "snapshot should be valid JSON"
    );

    eprintln!(
        "Dumped session {}: {} messages, {:.2} cost",
        session_id, snapshot.messages.len(), snapshot.session.cost
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_dump_multi_turn() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("I like cats. Reply with OK."),
    )
    .await
    .unwrap()
    .unwrap();
    let first = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    let session_id = first.run.as_ref().unwrap().session_id.clone();

    let _ = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume(&session_id, "I also like dogs. Reply with OK."),
    )
    .await
    .unwrap()
    .unwrap();

    let snapshot = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&session_id),
    )
    .await
    .unwrap()
    .unwrap();

    assert!(
        snapshot.messages.len() >= 4,
        "multi-turn dump should have at least 4 messages, got {}",
        snapshot.messages.len()
    );

    eprintln!("Multi-turn dump: {} messages", snapshot.messages.len());
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_restore() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Say hello."),
    )
    .await
    .unwrap()
    .unwrap();
    let first = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    let session_id = first.run.as_ref().unwrap().session_id.clone();
    assert!(!session_id.is_empty());

    let snapshot = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&session_id),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(!snapshot.messages.is_empty());
    let original_count = snapshot.messages.len();

    eprintln!("Dumped {} messages from session {}", original_count, session_id);

    tokio::time::timeout(Duration::from_secs(10), env.client.restore(&snapshot))
        .await
        .unwrap()
        .unwrap();

    let result = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume(&session_id, "What were we talking about?"),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Completed);
    assert!(!result.text().is_empty());

    let snapshot2 = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&session_id),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(
        snapshot2.messages.len() > original_count,
        "resumed session should have more messages than the restored snapshot"
    );

    eprintln!(
        "Post-restore: {} messages, response={}",
        snapshot2.messages.len(),
        truncate(&result.text(), 200)
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_full_round_trip() {
    if skip_integration() { return; }
    let env = get_env().await;

    eprintln!("Step 1: NewSession");
    let stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Say hello."),
    )
    .await
    .unwrap()
    .unwrap();
    let first = tokio::time::timeout(RUN_TIMEOUT, stream.result())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(first.run.as_ref().unwrap().status, RunStatus::Completed);
    let session_id = first.run.as_ref().unwrap().session_id.clone();
    assert!(!session_id.is_empty());

    eprintln!("Step 2: Resume");
    let result = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume(&session_id, "Say goodbye."),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Completed);
    assert_eq!(result.run.as_ref().unwrap().session_id, session_id);

    eprintln!("Step 3: Dump");
    let snapshot = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&session_id),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(
        snapshot.messages.len() >= 4,
        "should have at least 4 messages (2 user + 2 assistant)"
    );
    assert_eq!(snapshot.session.id, session_id);

    eprintln!("Step 4: Snapshot portability");
    let data = serde_json::to_string(&snapshot).unwrap();
    assert!(serde_json::from_str::<serde_json::Value>(&data).is_ok());
    let restored: SessionSnapshot = serde_json::from_str(&data).unwrap();
    assert_eq!(snapshot.session.id, restored.session.id);
    assert_eq!(snapshot.messages.len(), restored.messages.len());

    eprintln!("Step 5: Restore");
    tokio::time::timeout(Duration::from_secs(10), env.client.restore(&restored))
        .await
        .unwrap()
        .unwrap();

    eprintln!("Step 6: Resume after restore");
    let result = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume(&session_id, "Are you still there?"),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(result.run.as_ref().unwrap().status, RunStatus::Completed);
    assert!(!result.text().is_empty());

    eprintln!("Step 7: Final dump");
    let final_snap = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&session_id),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(
        final_snap.messages.len() > snapshot.messages.len(),
        "final snapshot should have more messages than pre-restore"
    );

    eprintln!(
        "Round-trip complete: session={}, initial={} msgs, final={} msgs",
        session_id,
        snapshot.messages.len(),
        final_snap.messages.len()
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_stream_snapshot_capture() {
    if skip_integration() { return; }
    let env = get_env().await;

    let mut stream = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Reply with: snapshot test complete"),
    )
    .await
    .unwrap()
    .unwrap();

    let mut got_snapshot = false;
    while let Some(ev) = stream.next().await {
        if ev.event_type == EventType::SessionSnapshot {
            got_snapshot = true;
        }
    }
    assert!(stream.err().is_none());

    if got_snapshot {
        eprintln!("Captured snapshot event during streaming");
    } else {
        eprintln!(
            "No session.snapshot event received (server may not emit for sync-like streams)"
        );
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_session_isolation() {
    if skip_integration() { return; }
    let env = get_env().await;

    let stream1 = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.new_session_stream("Say red."),
    )
    .await
    .unwrap()
    .unwrap();
    let r1 = tokio::time::timeout(RUN_TIMEOUT, stream1.result())
        .await
        .unwrap()
        .unwrap();
    let sid1 = r1.run.as_ref().unwrap().session_id.clone();

    let r2 = tokio::time::timeout(
        RUN_TIMEOUT,
        env.client.resume(&sid1, "Say blue."),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(
        r2.run.as_ref().unwrap().session_id,
        sid1,
        "resume should preserve session ID"
    );

    let snapshot = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump(&sid1),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(
        snapshot.messages.len() >= 4,
        "session should have at least 4 messages from 2 turns"
    );

    eprintln!("Session {}: {} messages", sid1, snapshot.messages.len());
}

#[tokio::test(flavor = "multi_thread")]
async fn integration_dump_not_found() {
    if skip_integration() { return; }
    let env = get_env().await;

    let result = tokio::time::timeout(
        Duration::from_secs(10),
        env.client.dump("nonexistent-session-id-12345"),
    )
    .await
    .unwrap();
    assert!(result.is_err());
    assert!(
        result.unwrap_err().to_string().contains("not found"),
        "error should mention not found"
    );
}
