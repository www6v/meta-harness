/**
 * Test DeepSeek harness with "who am i" prompt to verify tool events are shown
 */

const PLATFORM_URL = process.env.PLATFORM_URL || "http://127.0.0.1:8787";
const OMA_API_KEY = process.env.OMA_API_KEY || "dev-key";
const DEEPSEEK_GATEWAY = process.env.DEEPSEEK_GATEWAY || "http://127.0.0.1:3080";

const HEADERS = {
  "Content-Type": "application/json",
  "x-api-key": OMA_API_KEY,
  "x-user-id": "deepseek-whoami-test",
  "x-tenant-id": "default",
};

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function main() {
  console.log("=== DeepSeek 'Who Am I' Test ===\n");

  // Step 1: Create agent
  console.log("1. Creating DeepSeek agent...");
  const agentRes = await fetch(`${PLATFORM_URL}/v1/agents`, {
    method: "POST",
    headers: HEADERS,
    body: JSON.stringify({
      name: `deepseek-whoami-${Date.now()}`,
      model: { id: "deepseek-chat" },
      system: "You are a helpful assistant. Use tools when needed.",
      _oma: {
        harness: "deepseek",
      },
    }),
  });
  const agent = await agentRes.json();
  console.log(`   Agent: ${agent.id}\n`);

  // Step 2: Create session
  console.log("2. Creating session...");
  const sessionRes = await fetch(`${PLATFORM_URL}/v1/sessions`, {
    method: "POST",
    headers: HEADERS,
    body: JSON.stringify({
      agent: agent.id,
      environment_id: "env-local-default",
    }),
  });
  const session = await sessionRes.json();
  console.log(`   Session: ${session.id}\n`);

  // Step 3: Send "who am i" prompt
  console.log("3. Sending 'who am i' prompt...");
  const promptRes = await fetch(`${PLATFORM_URL}/v1/sessions/${session.id}/events`, {
    method: "POST",
    headers: HEADERS,
    body: JSON.stringify({
      events: [
        {
          type: "user.message",
          content: [{ type: "text", text: "who am i" }],
        },
      ],
    }),
  });
  console.log(`   Status: ${promptRes.status}\n`);

  // Step 4: Wait for turn completion
  console.log("4. Waiting for turn completion (timeout: 60s)...");
  const timeoutMs = 60000;
  const pollIntervalMs = 2000;
  const deadline = Date.now() + timeoutMs;
  let completed = false;

  while (Date.now() < deadline && !completed) {
    await sleep(pollIntervalMs);
    const eventsRes = await fetch(
      `${PLATFORM_URL}/v1/sessions/${session.id}/events?order=asc`,
      { headers: HEADERS }
    );
    const data = await eventsRes.json();
    const types = (data.data || []).map(e => e.type);

    if (types.includes("session.status_idle")) {
      completed = true;
      console.log("   Turn completed!\n");
      break;
    }
    process.stdout.write(".");
  }

  if (!completed) {
    console.error("\n   Timed out waiting for turn completion\n");
  }

  // Step 5: Show all events
  console.log("5. Event summary:");
  const finalRes = await fetch(
    `${PLATFORM_URL}/v1/sessions/${session.id}/events?order=asc`,
    { headers: HEADERS }
  );
  const finalData = await finalRes.json();
  const allEvents = finalData.data || [];

  console.log("\n   All events:");
  for (const ev of allEvents) {
    let content = "";
    if (ev.data.content?.[0]?.text) {
      content = ev.data.content[0].text.substring(0, 50);
    } else if (ev.data.name) {
      content = ev.data.name;
    } else if (ev.data.message?.content?.[0]?.text) {
      content = ev.data.message.content[0].text.substring(0, 50);
    } else if (ev.data.error) {
      content = JSON.stringify(ev.data.error).substring(0, 80);
    }
    console.log(`   - ${ev.type.padEnd(25)} ${content}`);
  }

  // Step 6: Check for tool events
  console.log("\n6. Checking for tool events:");
  const hasToolUse = allEvents.some(e => e.type === "agent.tool_use");
  const hasToolResult = allEvents.some(e => e.type === "agent.tool_result");
  const hasAgentMessage = allEvents.some(e => e.type === "agent.message");

  console.log(`   agent.tool_use: ${hasToolUse ? '✓' : '✗'}`);
  console.log(`   agent.tool_result: ${hasToolResult ? '✓' : '✗'}`);
  console.log(`   agent.message: ${hasAgentMessage ? '✓' : '✗'}`);

  if (!hasToolUse && !hasToolResult && !hasAgentMessage) {
    console.error("\n✗ No tool or message events found - this is the bug!\n");
    process.exit(1);
  }

  console.log("\n✓ Test passed!\n");
}

main().catch(e => {
  console.error(`\nTest failed: ${e.message}\n`);
  process.exit(1);
});
