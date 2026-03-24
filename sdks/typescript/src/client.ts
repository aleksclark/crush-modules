import { parseStream } from "./stream.js";
import type {
  AgentManifest,
  AgentsListResponse,
  AcpError,
  Event,
  ImportResponse,
  Message,
  Run,
  RunCreateRequest,
  SessionSnapshot,
} from "./types.js";
import { newUserMessage, textContent } from "./types.js";

const CONTENT_TYPE_NDJSON = "application/x-ndjson";

export interface ClientOptions {
  headers?: Record<string, string>;
  agentName?: string;
  fetch?: typeof globalThis.fetch;
}

export interface SessionResult {
  run: Run | null;
  snapshot: SessionSnapshot | null;
}

export function sessionResultText(result: SessionResult): string {
  if (!result.run) return "";
  return textContent(result.run.output);
}

export class Stream {
  private generator: AsyncGenerator<Event>;
  private _err: Error | null = null;
  private _lastRun: Run | null = null;
  private _snapshot: SessionSnapshot | null = null;

  constructor(generator: AsyncGenerator<Event>) {
    this.generator = generator;
  }

  async *events(): AsyncGenerator<Event> {
    for await (const event of this.generator) {
      if (event.run) {
        this._lastRun = event.run;
      }
      if (
        event.type === "session.snapshot" &&
        event.generic &&
        typeof event.generic === "object" &&
        "version" in event.generic
      ) {
        this._snapshot = event.generic as SessionSnapshot;
      }
      yield event;
    }
  }

  async result(): Promise<SessionResult> {
    for await (const _ of this.events()) {
      // drain
    }
    return { run: this._lastRun, snapshot: this._snapshot };
  }

  get err(): Error | null {
    return this._err;
  }
}

export class Client {
  private baseURL: string;
  private headers: Record<string, string>;
  private agentName: string | null;
  private fetchFn: typeof globalThis.fetch;

  constructor(baseURL: string, options: ClientOptions = {}) {
    this.baseURL = baseURL;
    this.headers = options.headers ?? {};
    this.agentName = options.agentName ?? null;
    this.fetchFn = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  async ping(): Promise<void> {
    const resp = await this.fetchFn(`${this.baseURL}/ping`, {
      headers: this.headers,
    });
    const body = await resp.text();
    if (resp.status !== 200 || body !== "pong") {
      throw new Error(
        `ping: unexpected response (HTTP ${resp.status}): ${body}`,
      );
    }
  }

  async listAgents(): Promise<AgentManifest[]> {
    const resp = await this.fetchFn(`${this.baseURL}/agents`, {
      headers: this.headers,
    });
    if (resp.status !== 200) {
      throw await this.readError(resp);
    }
    const result: AgentsListResponse = await resp.json();
    return result.agents;
  }

  async newSession(prompt: string): Promise<SessionResult> {
    return this.runSync(null, prompt);
  }

  async resume(sessionID: string, prompt: string): Promise<SessionResult> {
    if (!sessionID) {
      throw new Error("session ID is required for resume");
    }
    return this.runSync(sessionID, prompt);
  }

  async newSessionStream(prompt: string): Promise<Stream> {
    return this.runStream(null, prompt);
  }

  async resumeStream(sessionID: string, prompt: string): Promise<Stream> {
    if (!sessionID) {
      throw new Error("session ID is required for resumeStream");
    }
    return this.runStream(sessionID, prompt);
  }

  async dump(sessionID: string): Promise<SessionSnapshot> {
    const resp = await this.fetchFn(
      `${this.baseURL}/sessions/${sessionID}/export`,
      { headers: this.headers },
    );
    if (resp.status !== 200) {
      throw await this.readError(resp);
    }
    return resp.json();
  }

  async restore(snapshot: SessionSnapshot): Promise<void> {
    const resp = await this.fetchFn(`${this.baseURL}/sessions/import`, {
      method: "POST",
      headers: { ...this.headers, "Content-Type": "application/json" },
      body: JSON.stringify(snapshot),
    });
    if (resp.status !== 200) {
      throw await this.readError(resp);
    }
    const result: ImportResponse = await resp.json();
    if (result.status !== "imported") {
      throw new Error(`unexpected import status: ${result.status}`);
    }
  }

  async waitReady(intervalMs = 500, timeoutMs = 30000): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      try {
        await this.ping();
        return;
      } catch {
        if (Date.now() >= deadline) {
          throw new Error("server not ready: timeout");
        }
        await new Promise((r) => setTimeout(r, intervalMs));
      }
    }
  }

  private async resolveAgent(): Promise<string> {
    if (this.agentName) return this.agentName;
    const agents = await this.listAgents();
    if (agents.length === 0) {
      throw new Error(`no agents available on ${this.baseURL}`);
    }
    this.agentName = agents[0].name;
    return this.agentName;
  }

  private async runSync(
    sessionID: string | null,
    prompt: string,
  ): Promise<SessionResult> {
    const agent = await this.resolveAgent();
    const body: RunCreateRequest = {
      agent_name: agent,
      input: [newUserMessage(prompt)],
      session_id: sessionID ?? undefined,
      mode: "sync",
    };

    const resp = await this.fetchFn(`${this.baseURL}/runs`, {
      method: "POST",
      headers: { ...this.headers, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    if (resp.status !== 200 && resp.status !== 202) {
      throw await this.readError(resp);
    }

    const run: Run = await resp.json();
    return { run, snapshot: null };
  }

  private async runStream(
    sessionID: string | null,
    prompt: string,
  ): Promise<Stream> {
    const agent = await this.resolveAgent();
    const body: RunCreateRequest = {
      agent_name: agent,
      input: [newUserMessage(prompt)],
      session_id: sessionID ?? undefined,
      mode: "stream",
    };

    const resp = await this.fetchFn(`${this.baseURL}/runs`, {
      method: "POST",
      headers: {
        ...this.headers,
        "Content-Type": "application/json",
        Accept: CONTENT_TYPE_NDJSON,
      },
      body: JSON.stringify(body),
    });

    if (resp.status !== 200) {
      throw await this.readError(resp);
    }

    if (!resp.body) {
      throw new Error("response body is not a readable stream");
    }

    return new Stream(parseStream(resp.body));
  }

  private async readError(resp: Response): Promise<Error> {
    const body = await resp.text();
    try {
      const err: AcpError = JSON.parse(body);
      if (err.message) {
        return new Error(`ACP error (HTTP ${resp.status}): ${err.message}`);
      }
    } catch {
      // not JSON
    }
    return new Error(`ACP error (HTTP ${resp.status}): ${body}`);
  }
}
