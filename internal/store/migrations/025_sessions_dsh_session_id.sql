-- Add dsh_session_id column to sessions table for deepseek-harness session mapping
-- This enables per-session file isolation in deepseek-harness

ALTER TABLE sessions ADD COLUMN dsh_session_id TEXT;

CREATE INDEX idx_sessions_dsh_session_id ON sessions(dsh_session_id);

-- Comment explaining the purpose:
-- dsh_session_id stores the deepseek-harness session UUID that corresponds to this oma-session.
-- Each oma-session has its own isolated deepseek-harness session namespace, enabling:
-- - Per-session file isolation in workspaceFiles RPC calls
-- - Independent working directories per session
-- - Clean session lifecycle management (create/delete follows oma-session)
