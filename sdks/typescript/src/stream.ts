import type { Event, AcpError } from "./types.js";

const SSE_PREFIXES = ["data:", "event:", "id:", "retry:", ":"];

function looksLikeSSE(line: string): boolean {
  if (line === "[DONE]") return true;
  return SSE_PREFIXES.some((p) => line.startsWith(p));
}

function truncatePrefix(s: string, n: number): string {
  return s.length <= n ? s : s.slice(0, n) + "...";
}

export async function* parseStream(
  body: ReadableStream<Uint8Array>,
): AsyncGenerator<Event> {
  const reader = body.pipeThrough(new TextDecoderStream() as TransformStream<Uint8Array, string>).getReader();
  let buf = "";

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += value;

    let newlineIdx: number;
    while ((newlineIdx = buf.indexOf("\n")) !== -1) {
      const line = buf.slice(0, newlineIdx);
      buf = buf.slice(newlineIdx + 1);

      if (line === "") continue;

      if (looksLikeSSE(line)) {
        yield {
          type: "error",
          error: {
            message: `server sent SSE-formatted data instead of NDJSON (got line starting with ${JSON.stringify(truncatePrefix(line, 40))}) — the ACP server must use streamable HTTP (application/x-ndjson), not SSE (text/event-stream)`,
          },
        };
        continue;
      }

      try {
        yield JSON.parse(line) as Event;
      } catch (e) {
        yield {
          type: "error",
          error: {
            message: `failed to parse event: ${e instanceof Error ? e.message : String(e)}`,
          } satisfies AcpError,
        };
      }
    }
  }

  if (buf.trim()) {
    try {
      yield JSON.parse(buf.trim()) as Event;
    } catch (e) {
      yield {
        type: "error",
        error: {
          message: `failed to parse event: ${e instanceof Error ? e.message : String(e)}`,
        } satisfies AcpError,
      };
    }
  }
}
