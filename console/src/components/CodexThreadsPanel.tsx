import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { formatRelative } from '../lib/format';

interface CodexThread {
  id: string;
  name: string;
  status: string;
  created_at: number;
  updated_at: number;
}

interface CodexThreadsPanelProps {
  sessionId: string;
}

export function CodexThreadsPanel({ sessionId }: CodexThreadsPanelProps) {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['codex-threads', sessionId],
    queryFn: async () => {
      const res = await fetch(`/v1/codex/threads?session_id=${sessionId}`);
      if (!res.ok) throw new Error('Failed to fetch Codex threads');
      return res.json();
    },
    refetchInterval: 10000, // Refresh every 10 seconds
  });

  const threads: CodexThread[] = data?.threads || [];

  if (isLoading) {
    return (
      <div className="rounded-lg border bg-card p-4">
        <h3 className="text-sm font-semibold mb-3">Codex Threads</h3>
        <div className="text-sm text-muted-foreground">Loading...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border bg-card p-4">
        <h3 className="text-sm font-semibold mb-3">Codex Threads</h3>
        <div className="text-sm text-destructive">
          Error: {(error as Error).message}
        </div>
      </div>
    );
  }

  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold">Codex Threads</h3>
        <button
          onClick={() => refetch()}
          className="text-xs text-primary hover:underline"
        >
          Refresh
        </button>
      </div>
      
      {threads.length === 0 ? (
        <div className="text-sm text-muted-foreground">No Codex threads found</div>
      ) : (
        <div className="space-y-2">
          {threads.map((thread) => (
            <Link
              key={thread.id}
              to={`/sessions/${sessionId}/threads/${thread.id}`}
              className="block rounded-md border p-3 hover:bg-accent transition-colors"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="text-sm font-medium truncate">
                      {thread.name || thread.id}
                    </span>
                    <span className={`text-xs px-1.5 py-0.5 rounded ${
                      thread.status === 'active'
                        ? 'bg-green-100 text-green-800'
                        : thread.status === 'idle'
                        ? 'bg-gray-100 text-gray-800'
                        : 'bg-blue-100 text-blue-800'
                    }`}>
                      {thread.status}
                    </span>
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Updated {formatRelative(thread.updated_at * 1000)}
                  </div>
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
