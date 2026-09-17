/**
 * Typert RPC client for deepseek-harness file operations.
 *
 * Implements the Typert RPC protocol over HTTP:
 * - Request: { type: "client-request", rpcId: string, method: string, payload: { args: {...} } }
 * - Response: { type: "server-response", rpcId: string, result: { ok: boolean, value?: ..., error?: ... } }
 *
 * File upload uses fileUploads/upload RPC or POST /api/session/uploadFileBinary
 * Session outputs use workspaceFiles RPC endpoints
 */

// Generate a unique RPC ID for each request
function generateRpcId(): string {
  return `rpc-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

/**
 * Typert RPC request envelope
 */
interface ClientRequest {
  type: "client-request";
  rpcId: string;
  method: string;
  payload: { args: Record<string, unknown> };
}

/**
 * Typert RPC response envelope
 */
interface ServerResponseSuccess<T> {
  type: "server-response";
  rpcId: string;
  result: {
    ok: true;
    value: T;
  };
}

interface ServerResponseFailure {
  type: "server-response";
  rpcId: string;
  result: {
    ok: false;
    error: {
      code: string;
      message: string;
      details?: Record<string, unknown>;
    };
  };
}

type ServerResponse<T> = ServerResponseSuccess<T> | ServerResponseFailure;

/**
 * File upload response from deepseek-harness
 */
export interface FileUploadResponse {
  receiptId: string;
  file: {
    attachmentId: string;
    name: string;
    bytes: number;
  };
}

/**
 * Session output file entry
 */
export interface SessionOutputFile {
  filename: string;
  size_bytes: number;
  uploaded_at: string;
  media_type: string;
}

/**
 * Options for file upload
 */
export interface FileUploadOptions {
  name?: string;
  sessionId: string;
}

/**
 * Typert RPC client for deepseek-harness
 */
export class DeepSeekRpcClient {
  private gatewayUrl: string;
  private token?: string;

  constructor(gatewayUrl: string, token?: string) {
    this.gatewayUrl = gatewayUrl.replace(/\/$/, ""); // Trim trailing slash
    this.token = token;
  }

  /**
   * Send a unary RPC request over HTTP POST /api/<method>
   */
  private async rpc<T>(method: string, args: Record<string, unknown>): Promise<T> {
    const rpcId = generateRpcId();
    const methodPath = method.replace(/\./g, "/");

    const body: ClientRequest = {
      type: "client-request",
      rpcId,
      method: methodPath,
      payload: { args },
    };

    const response = await fetch(`${this.gatewayUrl}/api/${methodPath}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(this.token ? { Authorization: `Bearer ${this.token}` } : {}),
      },
      credentials: "include", // Include cookies for auth
      body: JSON.stringify(body),
    });

    if (!response.ok) {
      const raw = await response.text().catch(() => "unknown error");
      throw new Error(`deepseek rpc ${method} status=${response.status}: ${raw}`);
    }

    const envelope: ServerResponse<T> = await response.json();

    // Type guard to narrow the union
    if (!envelope.result.ok) {
      const result = envelope.result as ServerResponseFailure["result"];
      throw new Error(`deepseek rpc ${method}: ${result.error.code ?? "unknown"}: ${result.error.message ?? "unknown error"}`);
    }

    const successResult = envelope.result as ServerResponseSuccess<T>["result"];
    return successResult.value;
  }

  /**
   * Upload a file using fileUploads/upload RPC
   *
   * This is the base64-encoded RPC method. For large files, use uploadFileBinary instead.
   */
  async uploadFile(
    file: File,
    options: FileUploadOptions
  ): Promise<FileUploadResponse> {
    // Convert file to base64
    const base64Data = await this.fileToBase64(file);

    return this.rpc<FileUploadResponse>("fileUploads.upload", {
      sessionId: options.sessionId,
      request: {
        data: base64Data,
        name: options.name ?? file.name,
      },
    });
  }

  /**
   * Upload a file using binary POST /api/session/uploadFileBinary
   *
   * Preferred for large files as it avoids base64 overhead.
   */
  async uploadFileBinary(
    file: File,
    sessionId: string,
    name?: string
  ): Promise<FileUploadResponse> {
    const params = new URLSearchParams({ sessionId });
    if (name) params.set("name", name);

    const response = await fetch(`${this.gatewayUrl}/api/session/uploadFileBinary?${params.toString()}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        ...(this.token ? { Authorization: `Bearer ${this.token}` } : {}),
      },
      credentials: "include", // Include cookies for auth
      body: file,
    });

    if (!response.ok) {
      const raw = await response.text().catch(() => "unknown error");
      throw new Error(`file upload binary status=${response.status}: ${raw}`);
    }

    const result = await response.json();

    if (!result.ok) {
      throw new Error(`file upload binary: ${result.error?.code ?? "unknown"}: ${result.error?.message ?? "unknown error"}`);
    }

    return result.value;
  }

  /**
   * List session output files using deepseek-harness workspaceFiles/list RPC
   *
   * This calls POST /api/workspaceFiles/list which is proxied by oma-server's
   * DSH proxy with proper auth cookie injection.
   *
   * Since each session has its own isolated cwd (dsh-sessions/{sessionId}),
   * we use "." to list files at the root of the session's workspace.
   * Agent writes to /mnt/session/outputs/ appear here directly.
   */
  async listSessionOutputs(sessionId: string): Promise<SessionOutputFile[]> {
    const result = await this.rpc<{
      entries: Array<{
        name: string;
        type: string;
        size?: number;
        path?: string;
      }>;
    }>("workspaceFiles/list", {
      workspaceFileScopeId: sessionId,
      path: ".",  // List root of session's isolated workspace
    });

    // Filter to only files (not directories) and convert format
    return (result.entries || [])
      .filter((e: { type: string }) => e.type === "file")
      .map((f: { name: string; size?: number; path?: string }) => ({
        filename: f.name,
        size_bytes: Number(f.size ?? 0),
        uploaded_at: new Date().toISOString(),
        media_type: "application/octet-stream",
      }));
  }

  /**
   * Read a session output file as bytes using deepseek-harness workspaceFiles/readAll RPC
   *
   * This calls POST /api/workspaceFiles/readAll which is proxied by oma-server's
   * DSH proxy with proper auth cookie injection.
   *
   * Since each session has its own isolated cwd (dsh-sessions/{sessionId}),
   * files are at the root of the session's workspace.
   */
  async readSessionOutputFileBytes(
    sessionId: string,
    filename: string
  ): Promise<{ bytes: Uint8Array; mediaType: string }> {
    const result = await this.rpc<{
      data: string; // base64
      eof: boolean;
      absolutePath: string;
      version: string;
    }>("workspaceFiles/readAll", {
      workspaceFileScopeId: sessionId,
      path: filename,  // File is at root of session's isolated workspace
    });

    // Decode base64 to bytes
    const binaryString = atob(result.data);
    const bytes = new Uint8Array(binaryString.length);
    for (let i = 0; i < binaryString.length; i++) {
      bytes[i] = binaryString.charCodeAt(i);
    }

    return {
      bytes,
      mediaType: this.guessMediaType(filename),
    };
  }

  /**
   * Guess media type from file extension
   */
  private guessMediaType(path: string): string {
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

  /**
   * List files in a workspace directory
   *
   * @param path - Directory path on the deepseek-harness local filesystem
   * @returns Array of file entries with metadata
   */
  async listWorkspaceFiles(
    path: string
  ): Promise<Array<{
    path: string;
    size: number;
    modified: string;
    isDirectory: boolean;
  }>> {
    const result = await this.rpc<{
      entries: Array<{
        path: string;
        size: number;
        modified: string;
        isDirectory: boolean;
      }>;
    }>("workspaceFiles.list", { path });

    return result.entries;
  }

  /**
   * Read any file from the deepseek-harness local filesystem
   *
   * @param filePath - Full path on the deepseek-harness local filesystem
   * @returns File content as string with media type
   */
  async readWorkspaceFile(
    filePath: string
  ): Promise<{ content: string; mediaType: string }> {
    const result = await this.rpc<{
      content: string;
      encoding: string;
    }>("workspaceFiles.readAll", { path: filePath });

    return {
      content: result.content,
      mediaType: this.guessMediaType(filePath),
    };
  }

  /**
   * Read any file from the deepseek-harness local filesystem as bytes
   *
   * @param filePath - Full path on the deepseek-harness local filesystem
   * @returns File content as Uint8Array with media type
   */
  async readWorkspaceFileBytes(
    filePath: string
  ): Promise<{ bytes: Uint8Array; mediaType: string }> {
    const result = await this.rpc<{
      content: string; // base64
      encoding: "base64";
      totalSize: number;
    }>("workspaceFiles.readBytes", {
      path: filePath,
      offset: 0,
    });

    // Decode base64 to bytes
    const binaryString = atob(result.content);
    const bytes = new Uint8Array(binaryString.length);
    for (let i = 0; i < binaryString.length; i++) {
      bytes[i] = binaryString.charCodeAt(i);
    }

    return {
      bytes,
      mediaType: this.guessMediaType(filePath),
    };
  }

  /**
   * Get file stat/metadata
   *
   * @param filePath - Full path on the deepseek-harness local filesystem
   * @returns File metadata including size, modification time, etc.
   */
  async statWorkspaceFile(
    filePath: string
  ): Promise<{
    path: string;
    size: number;
    modified: string;
    isDirectory: boolean;
  }> {
    return this.rpc<{
      path: string;
      size: number;
      modified: string;
      isDirectory: boolean;
    }>("workspaceFiles.stat", { path: filePath });
  }

  /**
   * Convert a File to base64 string (without data: URL prefix)
   */
  private async fileToBase64(file: File): Promise<string> {
    const buffer = await file.arrayBuffer();
    const bytes = new Uint8Array(buffer);

    // Convert bytes to base64
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
  }
}

/**
 * Default RPC client instance
 * Uses the same gateway as the console's API
 */
let defaultClient: DeepSeekRpcClient | undefined;

export function getDefaultRpcClient(): DeepSeekRpcClient {
  if (!defaultClient) {
    // Use the same base URL as the console API
    const baseUrl = window.location.origin;
    defaultClient = new DeepSeekRpcClient(baseUrl);
  }
  return defaultClient;
}

/**
 * Set the default RPC client (useful for testing or custom configurations)
 */
export function setDefaultRpcClient(client: DeepSeekRpcClient): void {
  defaultClient = client;
}
