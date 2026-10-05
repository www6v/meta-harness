import { useQuery } from '@tanstack/react-query';

interface MCPServer {
  name: string;
  status: string;
  error?: string;
  tool_count?: number;
  resource_count?: number;
}

interface MCPStatusPanelProps {
  sessionId: string;
}

export function MCPStatusPanel({ sessionId }: MCPStatusPanelProps) {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['mcp-status', sessionId],
    queryFn: async () => {
      const res = await fetch(`/v1/codex/mcp/status?session_id=${sessionId}`);
      if (!res.ok) throw new Error('Failed to fetch MCP status');
      return res.json();
    },
    refetchInterval: 5000, // Refresh every 5 seconds
  });

  const servers: MCPServer[] = data?.servers || [];

  if (isLoading) {
    return (
      <div className="rounded-lg border bg-card p-4">
        <h3 className="text-sm font-semibold mb-3">MCP Servers</h3>
        <div className="text-sm text-muted-foreground">Loading...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border bg-card p-4">
        <h3 className="text-sm font-semibold mb-3">MCP Servers</h3>
        <div className="text-sm text-destructive">
          Error: {(error as Error).message}
        </div>
      </div>
    );
  }

  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold">MCP Servers</h3>
        <button
          onClick={() => refetch()}
          className="text-xs text-primary hover:underline"
        >
          Refresh
        </button>
      </div>
      
      {servers.length === 0 ? (
        <div className="text-sm text-muted-foreground">No MCP servers configured</div>
      ) : (
        <div className="space-y-2">
          {servers.map((server) => (
            <div
              key={server.name}
              className="flex items-center justify-between rounded-md border p-2"
            >
              <div className="flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{server.name}</span>
                  <span className={`text-xs px-1.5 py-0.5 rounded ${
                    server.status === 'connected'
                      ? 'bg-green-100 text-green-800'
                      : server.status === 'disconnected'
                      ? 'bg-gray-100 text-gray-800'
                      : 'bg-red-100 text-red-800'
                  }`}>
                    {server.status}
                  </span>
                </div>
                {server.error && (
                  <div className="text-xs text-destructive mt-1">
                    {server.error}
                  </div>
                )}
                {server.tool_count !== undefined && (
                  <div className="text-xs text-muted-foreground mt-1">
                    {server.tool_count} tools, {server.resource_count || 0} resources
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
