//! # Crush ACP SDK for Rust
//!
//! A high-level Rust client for the Crush Agent Communication Protocol (ACP).
//! Provides session-oriented methods for multi-turn conversations with Crush
//! agents, including full session persistence via export/import.
//!
//! # Quick Start
//!
//! ```rust,no_run
//! use crush_acp_sdk::Client;
//!
//! #[tokio::main]
//! async fn main() -> Result<(), Box<dyn std::error::Error>> {
//!     let client = Client::new("http://localhost:8199");
//!
//!     // Start a new session.
//!     let result = client.new_session("Fix the login bug").await?;
//!     println!("{}", result.text());
//!     println!("Session: {}", result.run.as_ref().unwrap().session_id);
//!
//!     // Continue the conversation.
//!     let session_id = result.run.as_ref().unwrap().session_id.clone();
//!     let result = client.resume(&session_id, "Now add tests").await?;
//!     println!("{}", result.text());
//!     Ok(())
//! }
//! ```
//!
//! # Streaming
//!
//! ```rust,no_run
//! use crush_acp_sdk::{Client, EventType};
//!
//! #[tokio::main]
//! async fn main() -> Result<(), Box<dyn std::error::Error>> {
//!     let client = Client::new("http://localhost:8199");
//!
//!     let mut stream = client.new_session_stream("Explain auth.go").await?;
//!     while let Some(event) = stream.next().await {
//!         match event.event_type {
//!             EventType::MessagePart => {
//!                 if let Some(ref part) = event.part {
//!                     print!("{}", part.content);
//!                 }
//!             }
//!             EventType::RunCompleted => println!("\nDone"),
//!             _ => {}
//!         }
//!     }
//!     if let Some(err) = stream.err() {
//!         eprintln!("Error: {}", err);
//!     }
//!     Ok(())
//! }
//! ```

mod client;
mod stream;
mod types;

pub use client::{Client, ClientBuilder, SessionResult, Stream};
pub use types::*;
