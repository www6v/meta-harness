/**
 * WorkspaceFilesPanel - Browse and download files from deepseek-harness local filesystem
 *
 * Uses workspaceFiles RPC endpoints to:
 * - List files in a directory
 * - Read file content
 * - Download files to browser
 */

import { useEffect, useState } from "react";
import { DeepSeekRpcClient } from "../../lib/deepseek-rpc-client";
import { useApi } from "../../lib/api";

interface WorkspaceFileEntry {
  path: string;
  size: number;
  modified: string;
  isDirectory: boolean;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function formatPath(path: string): string {
  // Extract just the filename from the full path
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

function guessMediaType(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase();
  const mediaTypes: Record<string, string> = {
    txt: "text/plain",
    html: "text/html",
    htm: "text/html",
    css: "text/css",
    js: "application/javascript",
    json: "application/json",
    xml: "application/xml",
    csv: "text/csv",
    md: "text/markdown",
    pdf: "application/pdf",
    png: "image/png",
    jpg: "image/jpeg",
    jpeg: "image/jpeg",
    gif: "image/gif",
    svg: "image/svg+xml",
    webp: "image/webp",
    mp4: "video/mp4",
    mp3: "audio/mpeg",
    zip: "application/zip",
    tar: "application/x-tar",
    gz: "application/gzip",
  };
  return mediaTypes[ext ?? ""] ?? "application/octet-stream";
}

interface WorkspaceFilesPanelProps {
  onClose: () => void;
  /** Session ID to scope file browsing */
  sessionId: string;
}

export function WorkspaceFilesPanel({
  onClose,
  sessionId,
}: WorkspaceFilesPanelProps) {
  const { api } = useApi();
  const [files, setFiles] = useState<WorkspaceFileEntry[]>([]);
  const [currentPath, setCurrentPath] = useState(``);
  const [pathInput, setPathInput] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null);

  // RPC client for deepseek-harness
  const rpcClient = useState(() => new DeepSeekRpcClient(window.location.origin))[0];

  // Load session files on mount
  useEffect(() => {
    setFiles([]);
    setErr(null);
    setLoading(true);

    // List files in session scope - deepseek-harness stores session files
    // in the session's working directory or outputs directory
    rpcClient.listSessionOutputs(sessionId)
      .then((entries) => {
        // Sort: directories first, then files
        const sorted = entries.sort((a, b) => {
          if (a.isDirectory && !b.isDirectory) return -1;
          if (!a.isDirectory && b.isDirectory) return 1;
          return a.filename.localeCompare(b.filename);
        });
        setFiles(sorted as unknown as WorkspaceFileEntry[]);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  }, [sessionId, rpcClient]);

  const handleNavigate = (newPath: string) => {
    setPathInput(newPath);
    setCurrentPath(newPath);
  };

  const handlePathSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setCurrentPath(pathInput);
  };

  const handleDirectoryClick = (entry: WorkspaceFileEntry) => {
    if (entry.isDirectory) {
      handleNavigate(entry.path);
    }
  };

  const handleGoUp = () => {
    // Go up one directory
    const parts = currentPath.split(/[\\/]/);
    if (parts.length > 1) {
      parts.pop();
      handleNavigate(parts.join("\\"));
    }
  };

  const handleDownload = async (entry: WorkspaceFileEntry) => {
    setDownloading(entry.path);
    try {
      // Use session-scoped read
      const result = await rpcClient.readSessionOutputFileBytes(sessionId, entry.path);
      // Create blob and download
      const blob = new Blob([result.bytes], { type: result.mediaType });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = entry.path;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch (e) {
      console.error("download failed", entry.path, e);
    }
    setDownloading(null);
  };

  return (
    <aside className="w-[480px] shrink-0 bg-bg-surface/30 flex flex-col min-h-0 border-l border-border">
      <div className="px-4 py-3 flex items-start gap-3 shrink-0 border-b border-border">
        <div className="min-w-0 flex-1">
          <div className="text-[10px] uppercase tracking-wide text-fg-subtle font-mono">
            Session Files
          </div>
          <div className="text-base font-semibold text-fg">Session-scoped Files</div>
          <div className="text-xs text-fg-muted mt-0.5">
            Files generated or accessed during this session
          </div>
        </div>
        <button
          onClick={onClose}
          className="text-fg-subtle hover:text-fg-muted text-lg leading-none inline-flex items-center justify-center min-w-11 min-h-11 sm:min-w-8 sm:min-h-8 rounded hover:bg-bg-surface transition-colors"
          title="Close"
          aria-label="Close panel"
        >
          ×
        </button>
      </div>

      {/* File list */}
      <div className="flex-1 overflow-y-auto p-4 text-xs">
        {err && (
          <div className="text-danger p-2">
            Failed to load: {err}
            <div className="mt-1 text-[10px] text-fg-muted">
              Note: This requires the deepseek-harness gateway to be running and accessible.
            </div>
          </div>
        )}
        {loading && <div className="text-fg-subtle">Loading...</div>}
        {!loading && !err && files.length === 0 && (
          <div className="text-fg-subtle">
            No session files found. The agent must generate files during this session for them to appear here.
          </div>
        )}
        {files.length > 0 && (
          <ul className="space-y-1">
            {files.map((f) => (
              <li
                key={f.path}
                className="flex items-center gap-2 py-1.5 px-2 hover:bg-bg-surface/50 rounded transition-colors"
              >
                <span className="text-base w-5 text-center">

                </span>
                <div className="min-w-0 flex-1">
                  <div className="font-mono text-fg truncate block text-left w-full" title={f.path}>
                    {f.path}
                  </div>
                  <div className="text-[10px] text-fg-subtle mt-0.5">
                    {formatBytes(f.size)} · {new Date(f.modified).toLocaleString()}
                  </div>
                </div>
                <button
                  onClick={() => handleDownload(f)}
                  disabled={downloading === f.path}
                  className="px-2 py-1 text-[10px] bg-info/10 text-info rounded hover:bg-info/20 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  title="Download file"
                >
                  {downloading === f.path ? "..." : "↓"}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}
