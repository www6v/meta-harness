import { MCPStatusPanel } from './MCPStatusPanel';
import { CodexThreadsPanel } from './CodexThreadsPanel';

interface CodexIntegrationPanelProps {
  sessionId: string;
}

/**
 * CodexIntegrationPanel shows MCP server status and Codex thread sync
 * for a given session. Integrates the Codex MCP functionality into the
 * meta-harness console UI.
 */
export function CodexIntegrationPanel({ sessionId }: CodexIntegrationPanelProps) {
  return (
    <div className="space-y-4">
      <MCPStatusPanel sessionId={sessionId} />
      <CodexThreadsPanel sessionId={sessionId} />
    </div>
  );
}
