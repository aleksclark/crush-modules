import { describe, it, expect } from "vitest";
import { parseStream } from "./stream.js";
import type { Event } from "./types.js";

function streamFrom(text: string): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      controller.enqueue(encoder.encode(text));
      controller.close();
    },
  });
}

async function collectEvents(text: string): Promise<Event[]> {
  const events: Event[] = [];
  for await (const event of parseStream(streamFrom(text))) {
    events.push(event);
  }
  return events;
}

describe("parseStream", () => {
  it("parses basic NDJSON events", async () => {
    const stream = [
      '{"type":"run.created","run":{"agent_name":"echo","run_id":"r1","session_id":"","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}',
      '{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}',
      '{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","session_id":"","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}',
    ].join("\n") + "\n";

    const events = await collectEvents(stream);
    expect(events).toHaveLength(3);
    expect(events[0].type).toBe("run.created");
    expect(events[1].type).toBe("message.part");
    expect(events[1].part?.content).toBe("Hello");
    expect(events[2].type).toBe("run.completed");
  });

  it("skips empty lines", async () => {
    const stream = '{"type":"message.part","part":{"content_type":"text/plain","content":"ok"}}\n\n{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","session_id":"","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}\n';
    const events = await collectEvents(stream);
    expect(events).toHaveLength(2);
  });

  it("emits error for invalid JSON", async () => {
    const events = await collectEvents("not-valid-json\n");
    expect(events).toHaveLength(1);
    expect(events[0].type).toBe("error");
    expect(events[0].error?.message).toContain("failed to parse event");
  });

  it("returns nothing for empty stream", async () => {
    const events = await collectEvents("");
    expect(events).toHaveLength(0);
  });

  it("detects SSE-formatted data lines", async () => {
    const events = await collectEvents('data: {"type":"message.part"}\n');
    expect(events).toHaveLength(1);
    expect(events[0].type).toBe("error");
    expect(events[0].error?.message).toContain("server sent SSE-formatted data instead of NDJSON");
    expect(events[0].error?.message).toContain("application/x-ndjson");
  });

  it("detects SSE event lines", async () => {
    const events = await collectEvents("event: message\n");
    expect(events).toHaveLength(1);
    expect(events[0].type).toBe("error");
    expect(events[0].error?.message).toContain("SSE-formatted");
  });

  it("detects [DONE] marker", async () => {
    const events = await collectEvents("[DONE]\n");
    expect(events).toHaveLength(1);
    expect(events[0].type).toBe("error");
    expect(events[0].error?.message).toContain("SSE-formatted");
  });
});
