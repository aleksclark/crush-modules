import { describe, it, expect } from "vitest";
import {
  newUserMessage,
  newAgentMessage,
  textContent,
  isTerminalStatus,
} from "./types.js";
import type { Message, RunStatus } from "./types.js";

describe("newUserMessage", () => {
  it("creates a user message with text/plain content", () => {
    const msg = newUserMessage("hello");
    expect(msg.role).toBe("user");
    expect(msg.parts).toHaveLength(1);
    expect(msg.parts[0].content_type).toBe("text/plain");
    expect(msg.parts[0].content).toBe("hello");
  });
});

describe("newAgentMessage", () => {
  it("creates an agent message with text/plain content", () => {
    const msg = newAgentMessage("hi");
    expect(msg.role).toBe("agent");
    expect(msg.parts).toHaveLength(1);
    expect(msg.parts[0].content).toBe("hi");
  });
});

describe("textContent", () => {
  it("concatenates text/plain parts with newlines", () => {
    const messages: Message[] = [
      { role: "agent", parts: [{ content_type: "text/plain", content: "Hello" }] },
      { role: "agent", parts: [{ content_type: "text/plain", content: "World" }] },
    ];
    expect(textContent(messages)).toBe("Hello\nWorld");
  });

  it("returns empty string for no messages", () => {
    expect(textContent([])).toBe("");
  });

  it("skips non-text parts", () => {
    const messages: Message[] = [
      {
        role: "agent",
        parts: [
          { content_type: "image/png", content: "data" },
          { content_type: "text/plain", content: "caption" },
        ],
      },
    ];
    expect(textContent(messages)).toBe("caption");
  });
});

describe("isTerminalStatus", () => {
  it("identifies terminal statuses", () => {
    expect(isTerminalStatus("completed")).toBe(true);
    expect(isTerminalStatus("failed")).toBe(true);
    expect(isTerminalStatus("cancelled")).toBe(true);
  });

  it("identifies non-terminal statuses", () => {
    const nonTerminal: RunStatus[] = ["created", "in-progress", "awaiting", "cancelling"];
    for (const s of nonTerminal) {
      expect(isTerminalStatus(s)).toBe(false);
    }
  });
});
