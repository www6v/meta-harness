/**
 * End-to-end test for DeepSeek harness integration.
 *
 * This test verifies that:
 * 1. An agent with harness="deepseek" can be created
 * 2. A session can be started with the DeepSeek agent
 * 3. User messages are processed and agent responds
 * 4. Events are properly streamed and persisted
 *
 * Prerequisites:
 * - oma-server running at PLATFORM_URL (default: http://127.0.0.1:8787)
 * - DeepSeek gateway running at DEEPSEEK_GATEWAY (default: http://127.0.0.1:3080)
 * - OMA_API_KEY set (default: dev-key)
 *
 * Run: pnpm test -- deepseek-harness-e2e.test.ts
 */

import { describe, it, expect, beforeAll, afterAll, vi } from "vitest";
import type { AgentConfig, SessionMeta, SessionEvent } from "@meta-harness/api-types";

// Disable MSW for this E2E test - we need real network requests
vi.mock("./mocks/server", () => ({
  server: {
    listen: vi.fn(),
    resetHandlers: vi.fn(),
    close: vi.fn(),
  },
}));

const PLATFORM_URL = process.env.PLATFORM_URL || "http://127.0.0.1:8787";
const OMA_API_KEY = process.env.OMA_API_KEY || "dev-key";
const DEEPSEEK_GATEWAY = process.env.DEEPSEEK_GATEWAY || "http://127.0.0.1:3080";

const HEADERS = {
  "Content-Type": "application/json",
  "x-api-key": OMA_API_KEY,
  "x-user-id": "deepseek-e2e-test",
  "x-tenant-id": "default",
};

interface TestContext {
  agentId?: string;
  sessionId?: string;
  environmentId?: string;
}

const ctx: TestContext = {};

describe("DeepSeek Harness E2E", () => {
  beforeAll(async () => {
    // Check platform health
    const healthRes = await fetch(`${PLATFORM_URL}/health`);
    if (!healthRes.ok) {
      throw new Error(`Platform not healthy at ${PLATFORM_URL}`);
    }

    // Check DeepSeek gateway health
    try {
      const gatewayRes = await fetch(`${DEEPSEEK_GATEWAY}/api/session.list`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type: "client-request",
          rpcId: "health-check",
          method: "session.list",
          payload: {},
        }),
      });
      if (!gatewayRes.ok) {
        console.warn(`DeepSeek gateway not reachable at ${DEEPSEEK_GATEWAY}`);
      }
    } catch (e) {
      console.warn(`DeepSeek gateway connection error: ${e}`);
    }
  });

  afterAll(async () => {
    // Cleanup: archive agent
    if (ctx.agentId) {
      try {
        await fetch(`${PLATFORM_URL}/v1/agents/${ctx.agentId}/archive`, {
          method: "POST",
          headers: HEADERS,
        });
      } catch (e) {
        console.warn(`Failed to archive agent: ${e}`);
      }
    }
  });

  it("should create a DeepSeek agent", async () => {
    const agentName = `deepseek-e2e-${Date.now()}`;

    const res = await fetch(`${PLATFORM_URL}/v1/agents`, {
      method: "POST",
      headers: HEADERS,
      body: JSON.stringify({
        name: agentName,
        model: { id: "deepseek-chat", speed: "standard" },
        system: "You are a helpful assistant.",
        tools: [],
        _oma: {
          harness: "deepseek",
        },
      }),
    });

    expect(res.status).toBe(201);
    const agent: AgentConfig = await res.json();
    ctx.agentId = agent.id;

    // Verify harness binding
    expect(agent.harness).toBe("deepseek");
  });

  it("should list environments", async () => {
    const res = await fetch(`${PLATFORM_URL}/v1/environments?limit=5`, {
      headers: HEADERS,
    });

    expect(res.status).toBe(200);
    const data = await res.json();
    expect(data.data).toBeInstanceOf(Array);
    expect(data.data.length).toBeGreaterThan(0);

    ctx.environmentId = data.data[0].id;
  });

  it("should create a session with the DeepSeek agent", async () => {
    expect(ctx.agentId).toBeDefined();
    expect(ctx.environmentId).toBeDefined();

    const res = await fetch(`${PLATFORM_URL}/v1/sessions`, {
      method: "POST",
      headers: HEADERS,
      body: JSON.stringify({
        agent: ctx.agentId!,
        environment_id: ctx.environmentId!,
      }),
    });

    expect(res.status).toBe(201);
    const session: SessionMeta = await res.json();
    ctx.sessionId = session.id;

    expect(session.agent_id).toBe(ctx.agentId);
    expect(session.environment_id).toBe(ctx.environmentId);
  });

  it("should send a user message and get agent response", async () => {
    expect(ctx.sessionId).toBeDefined();

    // Send user message with explicit tool-use instruction
    const promptText = "Say hello and keep your response brief. Just say 'Hello from DeepSeek!'";

    const res = await fetch(`${PLATFORM_URL}/v1/sessions/${ctx.sessionId}/events`, {
      method: "POST",
      headers: HEADERS,
      body: JSON.stringify({
        events: [
          {
            type: "user.message",
            content: [{ type: "text", text: promptText }],
          },
        ],
      }),
    });

    // Accept 200, 201, or 202
    expect([200, 201, 202]).toContain(res.status);
  });

  it("should wait for turn completion", async () => {
    expect(ctx.sessionId).toBeDefined();

    const timeoutMs = 120000; // 2 minutes
    const pollIntervalMs = 2000;
    const deadline = Date.now() + timeoutMs;

    let completed = false;
    let errorSeen = false;

    while (Date.now() < deadline && !completed && !errorSeen) {
      await new Promise((r) => setTimeout(r, pollIntervalMs));

      const res = await fetch(
        `${PLATFORM_URL}/v1/sessions/${ctx.sessionId}/events?order=asc`,
        { headers: HEADERS }
      );

      if (!res.ok) continue;

      const data = await res.json();
      const events: SessionEvent[] = data.data || [];

      // Find last user.message index
      const lastUserIdx = events.findLastIndex((e) => e.type === "user.message");
      const tail = lastUserIdx >= 0 ? events.slice(lastUserIdx + 1) : events;
      const types = tail.map((e) => e.type);

      if (types.includes("session.error")) {
        errorSeen = true;
        break;
      }

      if (types.includes("session.status_idle") || types.includes("span.model_request_end")) {
        completed = true;
        break;
      }
    }

    expect(errorSeen).toBe(false);
    expect(completed).toBe(true);
  });

  it("should have agent.message in events", async () => {
    expect(ctx.sessionId).toBeDefined();

    const res = await fetch(
      `${PLATFORM_URL}/v1/sessions/${ctx.sessionId}/events?order=asc`,
      { headers: HEADERS }
    );

    expect(res.status).toBe(200);
    const data = await res.json();
    const events: SessionEvent[] = data.data || [];

    const eventTypes = events.map((e) => e.type);
    expect(eventTypes).toContain("agent.message");

    // Find and log the agent message
    const agentMessage = events.find((e) => e.type === "agent.message");
    expect(agentMessage).toBeDefined();

    if (agentMessage && "content" in agentMessage) {
      const textBlocks = agentMessage.content.filter((c) => c.type === "text");
      expect(textBlocks.length).toBeGreaterThan(0);
      console.log("Agent response:", textBlocks[0]?.text);
    }
  });

  it("should have span.model_request_end with usage", async () => {
    expect(ctx.sessionId).toBeDefined();

    const res = await fetch(
      `${PLATFORM_URL}/v1/sessions/${ctx.sessionId}/events?order=asc`,
      { headers: HEADERS }
    );

    expect(res.status).toBe(200);
    const data = await res.json();
    const events: SessionEvent[] = data.data || [];

    const spanEnd = events.find((e) => e.type === "span.model_request_end");
    expect(spanEnd).toBeDefined();

    if (spanEnd && "model_usage" in spanEnd) {
      const usage = spanEnd.model_usage;
      expect(usage).toBeDefined();
      expect(usage?.input_tokens).toBeGreaterThan(0);
      expect(usage?.output_tokens).toBeGreaterThan(0);
      console.log(`Token usage: input=${usage?.input_tokens}, output=${usage?.output_tokens}`);
    }
  });

  it("should get cost report", async () => {
    expect(ctx.agentId).toBeDefined();

    const res = await fetch(
      `${PLATFORM_URL}/v1/cost_report?limit=50`,
      { headers: HEADERS }
    );

    expect(res.status).toBe(200);
    const report = await res.json();

    expect(report.by_agent).toBeInstanceOf(Array);

    const agentReport = report.by_agent.find(
      (r: { agent_id: string }) => r.agent_id === ctx.agentId
    );

    if (agentReport) {
      expect(agentReport.input_tokens).toBeGreaterThan(0);
      expect(agentReport.output_tokens).toBeGreaterThan(0);
      console.log(
        `Cost report for agent: in=${agentReport.input_tokens}, out=${agentReport.output_tokens}`
      );
    }
  });
});
