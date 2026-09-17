/**
 * Manual E2E verification script for DeepSeek file RPC operations.
 * Tests the /api/* proxy from oma-server to deepseek gateway.
 *
 * Run: tsx console/src/manual-e2e-verify.ts
 */

const PLATFORM_URL = process.env.PLATFORM_URL || "http://127.0.0.1:8787";
const OMA_API_KEY = process.env.OMA_API_KEY || "dev-key";

const HEADERS = {
  "Content-Type": "application/json",
  "x-api-key": OMA_API_KEY,
  "x-user-id": "manual-e2e-verify",
  "x-tenant-id": "default",
};

async function main() {
  console.log("=== DeepSeek File RPC E2E Verification ===\n");

  // 1. Check platform health
  console.log("1. Checking platform health...");
  const healthRes = await fetch(`${PLATFORM_URL}/health`);
  if (!healthRes.ok) {
    console.error(`❌ Platform not healthy: ${healthRes.status}`);
    process.exit(1);
  }
  console.log("✅ Platform is healthy\n");

  // 2. Check DSH API proxy (should return 401 if no auth cookie, not 404)
  console.log("2. Checking DSH API proxy (/api/workspaceFiles/list)...");
  const rpcRes = await fetch(`${PLATFORM_URL}/api/workspaceFiles/list`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      type: "client-request",
      rpcId: "verify-1",
      method: "workspaceFiles/list",
      payload: { args: { path: "/mnt/session/outputs/test/" } },
    }),
  });

  if (rpcRes.status === 404) {
    console.error("❌ DSH API proxy not working - got 404 (proxy not configured)");
    process.exit(1);
  } else if (rpcRes.status === 401) {
    console.log("✅ DSH API proxy is working (401 = gateway requires auth cookie)");
    console.log("   Note: Browser will send auth cookie automatically\n");
  } else {
    console.log(`✅ DSH API proxy responded with status ${rpcRes.status}\n`);
  }

  // 3. Check file upload endpoint
  console.log("3. Checking file upload endpoint (/api/session/uploadFileBinary)...");
  const uploadRes = await fetch(`${PLATFORM_URL}/api/session/uploadFileBinary?sessionId=test&name=test.txt`, {
    method: "POST",
    headers: { "Content-Type": "application/octet-stream" },
    body: "test content",
  });

  if (uploadRes.status === 404) {
    console.error(" File upload endpoint not working - got 404");
    process.exit(1);
  } else if (uploadRes.status === 401) {
    console.log("✅ File upload endpoint is working (401 = gateway requires auth cookie)");
    console.log("   Note: Browser will send auth cookie automatically\n");
  } else {
    console.log(`✅ File upload endpoint responded with status ${uploadRes.status}\n`);
  }

  // 4. Create test agent and session
  console.log("4. Creating test agent...");
  const agentRes = await fetch(`${PLATFORM_URL}/v1/agents`, {
    method: "POST",
    headers: HEADERS,
    body: JSON.stringify({
      name: `e2e-verify-${Date.now()}`,
      model: { id: "deepseek-chat", speed: "standard" },
      system: "You are a helpful assistant.",
      tools: [],
      _oma: { harness: "deepseek" },
    }),
  });

  if (!agentRes.ok) {
    console.error(`❌ Failed to create agent: ${agentRes.status}`);
    process.exit(1);
  }
  const agent = await agentRes.json();
  console.log(`✅ Created agent: ${agent.id}\n`);

  // 5. Get environment
  console.log("5. Getting environment...");
  const envRes = await fetch(`${PLATFORM_URL}/v1/environments?limit=1`, {
    headers: HEADERS,
  });
  const envData = await envRes.json();
  const envId = envData.data?.[0]?.id;
  if (!envId) {
    console.error("❌ No environments available");
    process.exit(1);
  }
  console.log(`✅ Using environment: ${envId}\n`);

  // 6. Create session
  console.log("6. Creating session...");
  const sessionRes = await fetch(`${PLATFORM_URL}/v1/sessions`, {
    method: "POST",
    headers: HEADERS,
    body: JSON.stringify({
      agent: agent.id,
      environment_id: envId,
    }),
  });

  if (!sessionRes.ok) {
    console.error(`❌ Failed to create session: ${sessionRes.status}`);
    process.exit(1);
  }
  const session = await sessionRes.json();
  console.log(`✅ Created session: ${session.id}`);
  console.log(`   URL: ${PLATFORM_URL}/sessions/${session.id}\n`);

  // 7. Cleanup
  console.log("7. Cleaning up...");
  await fetch(`${PLATFORM_URL}/v1/agents/${agent.id}/archive`, {
    method: "POST",
    headers: HEADERS,
  });
  console.log("✅ Archived test agent\n");

  console.log("=== Verification Complete ===");
  console.log("\nNext steps:");
  console.log("1. Open browser to the session URL above");
  console.log("2. Ensure browser has authenticated with gateway (visit http://127.0.0.1:3080)");
  console.log("3. Test file upload using the + button");
  console.log("4. Ask agent to write a file to /mnt/session/outputs/");
  console.log("5. Click 'Output Files' to verify file listing and download");
}

main().catch((err) => {
  console.error("Fatal error:", err);
  process.exit(1);
});
