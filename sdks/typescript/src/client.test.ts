import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { createServer, type Server, type IncomingMessage, type ServerResponse } from "http";
import { Client, sessionResultText } from "./client.js";
import type { Run, AgentsListResponse } from "./types.js";

let server: Server;
let baseURL: string;
let handler: (req: IncomingMessage, res: ServerResponse) => void;

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (chunk: Buffer) => (data += chunk.toString()));
    req.on("end", () => resolve(data));
  });
}

beforeEach(
  () =>
    new Promise<void>((resolve) => {
      handler = (_req, res) => {
        res.writeHead(500);
        res.end("no handler");
      };
      server = createServer((req, res) => handler(req, res));
      server.listen(0, () => {
        const addr = server.address();
        if (addr && typeof addr === "object") {
          baseURL = `http://127.0.0.1:${addr.port}`;
        }
        resolve();
      });
    }),
);

afterEach(
  () =>
    new Promise<void>((resolve) => {
      server.close(() => resolve());
    }),
);

describe("Client", () => {
  describe("ping", () => {
    it("succeeds when server returns pong", async () => {
      handler = (_req, res) => {
        res.writeHead(200);
        res.end("pong");
      };
      const client = new Client(baseURL);
      await expect(client.ping()).resolves.toBeUndefined();
    });

    it("throws on non-200 response", async () => {
      handler = (_req, res) => {
        res.writeHead(503);
        res.end("not ready");
      };
      const client = new Client(baseURL);
      await expect(client.ping()).rejects.toThrow("unexpected response");
    });
  });

  describe("listAgents", () => {
    it("returns agents", async () => {
      handler = (_req, res) => {
        const body: AgentsListResponse = {
          agents: [{ name: "crush", description: "Crush AI" }],
        };
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify(body));
      };
      const client = new Client(baseURL);
      const agents = await client.listAgents();
      expect(agents).toHaveLength(1);
      expect(agents[0].name).toBe("crush");
    });
  });

  describe("newSession", () => {
    it("creates a session and returns result", async () => {
      handler = async (req, res) => {
        if (req.url === "/agents") {
          res.writeHead(200, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ agents: [{ name: "crush" }] }));
          return;
        }
        const body = await readBody(req);
        const parsed = JSON.parse(body);
        expect(parsed.agent_name).toBe("crush");
        expect(parsed.mode).toBe("sync");

        const run: Run = {
          agent_name: "crush",
          run_id: "run-1",
          session_id: "ses-abc",
          status: "completed",
          output: [
            {
              role: "agent",
              parts: [{ content_type: "text/plain", content: "Hi there!" }],
            },
          ],
          created_at: "2025-01-01T00:00:00Z",
        };
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify(run));
      };
      const client = new Client(baseURL);
      const result = await client.newSession("hello");
      expect(result.run?.session_id).toBe("ses-abc");
      expect(result.run?.status).toBe("completed");
      expect(sessionResultText(result)).toBe("Hi there!");
    });
  });

  describe("resume", () => {
    it("requires session ID", async () => {
      const client = new Client(baseURL);
      await expect(client.resume("", "hello")).rejects.toThrow(
        "session ID is required",
      );
    });
  });

  describe("newSessionStream", () => {
    it("streams events and collects result", async () => {
      handler = (req, res) => {
        if (req.url === "/agents") {
          res.writeHead(200, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ agents: [{ name: "crush" }] }));
          return;
        }
        res.writeHead(200, { "Content-Type": "application/x-ndjson" });
        const events = [
          '{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}',
          '{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}',
          '{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}',
          '{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"Hello World"}]}],"created_at":"2025-01-01T00:00:00Z"}}',
        ];
        res.end(events.join("\n") + "\n");
      };

      const client = new Client(baseURL);
      const stream = await client.newSessionStream("hi");

      const parts: string[] = [];
      for await (const ev of stream.events()) {
        if (ev.type === "message.part" && ev.part) {
          parts.push(ev.part.content);
        }
      }
      expect(parts).toEqual(["Hello", " World"]);
    });

    it("collects result via .result()", async () => {
      handler = (req, res) => {
        if (req.url === "/agents") {
          res.writeHead(200, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ agents: [{ name: "crush" }] }));
          return;
        }
        res.writeHead(200, { "Content-Type": "application/x-ndjson" });
        res.end(
          '{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"done"}]}],"created_at":"2025-01-01T00:00:00Z"}}\n',
        );
      };

      const client = new Client(baseURL);
      const stream = await client.newSessionStream("hi");
      const result = await stream.result();
      expect(result.run?.session_id).toBe("ses-1");
      expect(result.run?.status).toBe("completed");
    });
  });

  describe("dump", () => {
    it("exports a session snapshot", async () => {
      handler = (_req, res) => {
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(
          JSON.stringify({
            version: 1,
            session: { id: "ses-abc", title: "Test", message_count: 2, prompt_tokens: 0, completion_tokens: 0, cost: 0, created_at: 0, updated_at: 0 },
            messages: [
              { id: "m1", session_id: "ses-abc", role: "user", parts: "[]", created_at: 0, updated_at: 0 },
            ],
          }),
        );
      };
      const client = new Client(baseURL);
      const snap = await client.dump("ses-abc");
      expect(snap.version).toBe(1);
      expect(snap.session.id).toBe("ses-abc");
      expect(snap.messages).toHaveLength(1);
    });
  });

  describe("restore", () => {
    it("imports a session snapshot", async () => {
      handler = (_req, res) => {
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ session_id: "ses-abc", message_count: 1, status: "imported" }));
      };
      const client = new Client(baseURL);
      await expect(
        client.restore({
          version: 1,
          session: { id: "ses-abc", title: "", message_count: 0, prompt_tokens: 0, completion_tokens: 0, cost: 0, created_at: 0, updated_at: 0 },
          messages: [],
        }),
      ).resolves.toBeUndefined();
    });

    it("throws on non-imported status", async () => {
      handler = (_req, res) => {
        res.writeHead(400, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ code: 400, message: "snapshot version is required" }));
      };
      const client = new Client(baseURL);
      await expect(
        client.restore({
          version: 0,
          session: { id: "", title: "", message_count: 0, prompt_tokens: 0, completion_tokens: 0, cost: 0, created_at: 0, updated_at: 0 },
          messages: [],
        }),
      ).rejects.toThrow("snapshot version is required");
    });
  });

  describe("waitReady", () => {
    it("resolves when server becomes ready", async () => {
      let ready = false;
      setTimeout(() => (ready = true), 100);
      handler = (_req, res) => {
        if (!ready) {
          res.writeHead(503);
          res.end();
          return;
        }
        res.writeHead(200);
        res.end("pong");
      };
      const client = new Client(baseURL);
      await expect(client.waitReady(50, 2000)).resolves.toBeUndefined();
    });

    it("times out if server never ready", async () => {
      handler = (_req, res) => {
        res.writeHead(503);
        res.end();
      };
      const client = new Client(baseURL);
      await expect(client.waitReady(50, 200)).rejects.toThrow("not ready");
    });
  });

  describe("custom headers", () => {
    it("sends custom headers with requests", async () => {
      handler = (req, res) => {
        expect(req.headers["authorization"]).toBe("Bearer my-token");
        res.writeHead(200);
        res.end("pong");
      };
      const client = new Client(baseURL, {
        headers: { Authorization: "Bearer my-token" },
      });
      await client.ping();
    });
  });
});
