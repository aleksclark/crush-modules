export type RunStatus =
  | "created"
  | "in-progress"
  | "awaiting"
  | "completed"
  | "failed"
  | "cancelling"
  | "cancelled";

export type RunMode = "sync" | "async" | "stream";

export function isTerminalStatus(status: RunStatus): boolean {
  return status === "completed" || status === "failed" || status === "cancelled";
}

export interface AgentManifest {
  name: string;
  description?: string;
  input_content_types?: string[];
  output_content_types?: string[];
  metadata?: AgentMetadata;
}

export interface AgentMetadata {
  documentation?: string;
  framework?: string;
  capabilities?: AgentCapability[];
  tags?: string[];
}

export interface AgentCapability {
  name: string;
  description?: string;
}

export interface Message {
  role: string;
  parts: MessagePart[];
  created_at?: string;
  completed_at?: string;
}

export interface MessagePart {
  name?: string;
  content_type: string;
  content: string;
  content_encoding?: string;
  content_url?: string;
  metadata?: Record<string, unknown>;
}

export interface Run {
  agent_name: string;
  run_id: string;
  session_id: string;
  status: RunStatus;
  output: Message[];
  await_request?: AwaitRequest;
  error?: AcpError;
  created_at: string;
  finished_at?: string;
}

export interface AwaitRequest {
  message?: Message;
}

export interface AwaitResume {
  message?: Message;
}

export interface AcpError {
  code?: number;
  message: string;
  data?: unknown;
}

export type EventType =
  | "run.created"
  | "run.in-progress"
  | "run.awaiting"
  | "run.completed"
  | "run.failed"
  | "run.cancelled"
  | "message.created"
  | "message.part"
  | "message.completed"
  | "session.message"
  | "session.snapshot"
  | "error"
  | "generic";

export interface Event {
  type: EventType;
  run?: Run;
  message?: Message;
  part?: MessagePart;
  error?: AcpError;
  generic?: unknown;
}

export interface SessionData {
  id: string;
  title: string;
  summary_message_id?: string;
  message_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost: number;
  created_at: number;
  updated_at: number;
}

export interface SessionMessage {
  id: string;
  session_id: string;
  role: string;
  parts: string;
  model?: string;
  provider?: string;
  is_summary_message?: boolean;
  created_at: number;
  updated_at: number;
}

export interface SessionSnapshot {
  version: number;
  session: SessionData;
  messages: SessionMessage[];
}

export interface RunCreateRequest {
  agent_name: string;
  input: Message[];
  session_id?: string;
  mode?: RunMode;
}

export interface AgentsListResponse {
  agents: AgentManifest[];
}

export interface ImportResponse {
  session_id: string;
  message_count: number;
  status: string;
}

export function newUserMessage(text: string): Message {
  return {
    role: "user",
    parts: [{ content_type: "text/plain", content: text }],
  };
}

export function newAgentMessage(text: string): Message {
  return {
    role: "agent",
    parts: [{ content_type: "text/plain", content: text }],
  };
}

export function textContent(messages: Message[]): string {
  const parts: string[] = [];
  for (const msg of messages) {
    for (const part of msg.parts) {
      if (part.content_type === "text/plain" || part.content_type === "") {
        parts.push(part.content);
      }
    }
  }
  return parts.join("\n");
}
