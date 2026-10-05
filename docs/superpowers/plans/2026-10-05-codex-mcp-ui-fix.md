# Codex MCP UI Fix - Remove Session Tab, Align with Agent/Session Architecture

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove the incorrectly placed "Codex" tab from SessionDetail and align the UI with the correct architecture where MCP servers are configured at the Agent level and credentials at the Session level.

**Architecture:** 
- **Agent configuration**: MCP servers are added when creating/editing an agent (already implemented in "MCP Servers" tab)
- **Session configuration**: Credential vaults are selected when creating a session (already implemented)
- **Runtime**: When a session runs with an agent that has MCP servers, credentials from the selected vault are automatically injected
- **Backend APIs**: The `/v1/codex/mcp/status` and `/v1/codex/threads` endpoints remain for debugging/admin but are not exposed in the session UI

**Tech Stack:** React, TypeScript, Go

**Spec:** Based on user feedback and screenshots showing correct MCP/Credential placement in Agent and Session configuration forms.

## Global Constraints

- Keep all backend API endpoints (they're useful for debugging)
- Remove only the Session Detail "Codex" tab
- Do not break existing Agent MCP configuration UI
- Do not break existing Session credential vault selection UI

## Review Focus

1. **Agent MCP tab preserved**: The "MCP Servers" tab in Agent configuration must still work
2. **Session vault selection preserved**: The "Credential Vaults" section in Session creation must still work
3. **Backend APIs functional**: The `/v1/codex/*` endpoints must still respond
4. **No orphaned imports**: Removing Codex tab should clean up all related imports in SessionDetail
5. **TypeScript compilation**: No type errors after removal

---

### Task 1: Remove Codex Tab from SessionDetail

**Files:**
- Modify: `console/src/pages/SessionDetail.tsx`
- Delete: `console/src/components/CodexIntegrationPanel.tsx`
- Delete: `console/src/components/MCPStatusPanel.tsx`
- Delete: `console/src/components/CodexThreadsPanel.tsx`

**Interfaces:**
- Consumes: None
- Produces: Clean SessionDetail without Codex tab

- [ ] **Step 1: Remove CodexIntegrationPanel import**

```typescript
// Remove this line from SessionDetail.tsx imports
import { CodexIntegrationPanel } from "../components/CodexIntegrationPanel";
```

- [ ] **Step 2: Remove "codex" from View type**

```typescript
// Change from:
type View = "transcript" | "debug" | "timeline" | "team" | "chat" | "codex";

// To:
type View = "transcript" | "debug" | "timeline" | "team" | "chat";
```

- [ ] **Step 3: Remove Codex ViewTab**

Find and remove:
```tsx
<ViewTab
  label="Codex"
  active={view === "codex"}
  onClick={() => setView("codex")}
/>
```

- [ ] **Step 4: Remove Codex view rendering**

Find and remove:
```tsx
) : view === "codex" ? (
  id ? (
    <div className="p-4">
      <CodexIntegrationPanel sessionId={id} />
    </div>
  ) : null
```

- [ ] **Step 5: Run TypeScript type check**

```bash
cd console
npm run typecheck
```

Expected: No errors in SessionDetail.tsx (pre-existing errors in other files are OK)

- [ ] **Step 6: Delete Codex component files**

```bash
rm console/src/components/CodexIntegrationPanel.tsx
rm console/src/components/MCPStatusPanel.tsx
rm console/src/components/CodexThreadsPanel.tsx
```

- [ ] **Step 7: Commit**

```bash
git add console/src/pages/SessionDetail.tsx
git rm console/src/components/CodexIntegrationPanel.tsx console/src/components/MCPStatusPanel.tsx console/src/components/CodexThreadsPanel.tsx
git commit -m "fix: remove Codex tab from SessionDetail (MCP configured at Agent level)

The Codex tab was incorrectly added to Session Detail. MCP servers are
configured at the Agent level (in Agent edit form), and credentials are
selected at Session creation via Credential Vaults. Runtime injection
is automatic. Backend APIs remain for debugging.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Verify Backend APIs Still Work

**Files:**
- No changes needed - APIs remain functional

**Interfaces:**
- Consumes: None
- Produces: Verification that APIs still respond

- [ ] **Step 1: Start the services**

```bash
# Ensure bridge is running
curl http://localhost:8092/health

# Start Go server
cd meta-harness
./start-console.sh
```

- [ ] **Step 2: Test MCP status API**

```bash
curl "http://localhost:8787/v1/codex/mcp/status?session_id=test-session"
```

Expected: JSON response with MCP server status (or error if no session)

- [ ] **Step 3: Test threads API**

```bash
curl "http://localhost:8787/v1/codex/threads?session_id=test-session"
```

Expected: JSON response with thread list

- [ ] **Step 4: Verify UI doesn't show Codex tab**

Open browser to http://localhost:8787, navigate to a session, verify tabs are:
`Transcript | Debug | Timeline | Team` (no Codex tab)

- [ ] **Step 5: Ledger the result**

```
Task 2: Backend APIs verified, UI cleaned up
```

---

### Task 3: Update Documentation

**Files:**
- Modify: `CODEX_MCP_INTEGRATION_COMPLETE.md`

**Interfaces:**
- Consumes: None
- Produces: Updated architecture documentation

- [ ] **Step 1: Update architecture section**

Replace the architecture diagram with:

```markdown
## Correct Architecture

### Configuration Flow
1. **Agent Edit** → "MCP Servers" tab → Add MCP servers (GitHub, Linear, etc.)
2. **Session Create** → "Credential Vaults" → Select vault for credential injection
3. **Session Run** → Automatic credential injection when agent uses MCP servers

### API Endpoints (Backend/Debug)
- `GET /v1/codex/mcp/status` — Query MCP server status
- `POST /v1/codex/mcp/reload` — Reload MCP config
- `GET /v1/codex/threads` — List Codex threads
- `GET /v1/codex/threads/{id}` — Read thread details

These endpoints are available for debugging but not exposed in the main UI.
```

- [ ] **Step 2: Commit**

```bash
git add CODEX_MCP_INTEGRATION_COMPLETE.md
git commit -m "docs: update architecture to reflect Agent/Session MCP design

Clarify that MCP servers are configured at Agent level and credentials
at Session level. Backend APIs remain for debugging but are not in UI.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Summary

This plan fixes the UI architecture by:
1. Removing the incorrectly placed "Codex" tab from Session Detail
2. Keeping all backend APIs functional for debugging
3. Aligning with the correct flow: Agent (MCP config) + Session (Credentials) → Auto-injection
4. Updating documentation to reflect the correct architecture

**Estimated effort:** 0.25 days
**Risk:** Low (only removes UI, keeps all backend functionality)
