/**
 * EventDetail — full right-pane render for a single event.
 *
 * Uses ai-elements primitives (Message, Tool, Reasoning, Markdown).
 * Includes "View in Debug →" link for Transcript tab to cross-reference
 * the raw event in the Debug tab.
 *
 * Tool events support bidirectional pairing: selecting tool_use OR
 * tool_result shows the same Input + Output Tool card when a pair exists.
 * Other types use structured Input/Output sections via getEventIO.
 */

import { DownloadIcon, FileIcon, Link2Icon } from "lucide-react";
import { Markdown } from "../../components/Markdown";
import {
  Message,
  MessageContent,
} from "../../components/ai-elements/message";
import {
  Reasoning,
  ReasoningContent,
  ReasoningTrigger,
} from "../../components/ai-elements/reasoning";
import {
  Tool,
  ToolContent,
  ToolHeader,
  ToolInput,
  ToolOutput,
} from "../../components/ai-elements/tool";
import { formatEventIOValue, getEventIO } from "../../lib/event-io";
import type { Event } from "../../lib/events";
import { getMergedEventText } from "./EventRow";

export interface EventDetailProps {
  event: Event;
  /**
   * Paired tool_result event for tool_use events. Caller pre-pairs by id
   * (tool_use_id / mcp_tool_use_id / custom_tool_use_id) and passes the
   * result here so the Tool card shows input + output in one collapsible
   * block instead of two disconnected bubbles.
   */
  pairedResult?: Event;
  /**
   * Paired tool_use when the selected event is a tool_result (reverse pair).
   * When set, renders the same Tool card as selecting the use event.
   */
  pairedUse?: Event;
  /**
   * Upstream model error context for `session.error` events. The
   * SSE-delivered session.error payload only carries a generic
   * "No output generated. Check the stream for errors." message; the
   * actionable cause (rate limit, billing, model 4xx, etc.) lives on
   * the preceding `span.model_request_end` with `is_error=true`. Caller
   * walks the events array and pairs them, passing the looked-up cause
   * here so operators see the real reason inline without diving into
   * the timeline tab. Only meaningful when `event.type === "session.error"`.
   */
  modelErrorCause?: { error: string; model?: string };
  /**
   * Callback to switch to Debug tab and scroll to this event.
   * If provided, a "View in Debug →" link is shown in the detail header.
   */
  onViewInDebug?: () => void;
  /**
   * When this detail represents a group of merged consecutive
   * agent.message events (streaming token fragments), pass all events
   * here. The component concatenates their text into one continuous
   * string and renders a single Markdown Message so headings, tables,
   * and other Markdown syntax reconstruct correctly.
   */
  mergedEvents?: Event[];
  /**
   * Pre-computed longest text from the selected merged group (or
   * consecutive run).  When set, overrides the single-event text for
   * `agent.message` rendering so the detail pane always shows the full
   * response — matching the transcript row.
   */
  overrideText?: string;
}

export function EventDetail({
  event,
  pairedResult,
  pairedUse,
  modelErrorCause,
  onViewInDebug,
  mergedEvents,
  overrideText,
}: EventDetailProps) {
  // For agent.message and agent.thinking events, the transcript row may show
  // a merged/longest text while the single event has only a short fragment.
  // overrideText is the authoritative "show this text" prop from the parent —
  // it takes priority over everything else for these event types.
  const isAgentMessage = event.type === "agent.message";
  const isAgentThinking = event.type === "agent.thinking";

  // Merged consecutive agent.message / agent.thinking events are cumulative
  // streaming fragments — use the merged (final) text instead of the
  // primaryEvent (first, shortest fragment) so the detail pane matches
  // what the transcript row shows.
  const allAgentThinking =
    mergedEvents !== undefined &&
    mergedEvents.length > 1 &&
    mergedEvents.every((e) => e.type === "agent.thinking");

  let content: React.ReactNode;
  if (isAgentMessage && overrideText) {
    // overrideText from parent — always the longest/best text available.
    content = (
      <Message from="assistant" className="max-w-full">
        <MessageContent className="w-full">
          <Markdown>{overrideText}</Markdown>
        </MessageContent>
      </Message>
    );
  } else if (isAgentThinking && overrideText) {
    // Thinking override — show full consecutive thinking text.
    content = (
      <Reasoning defaultOpen>
        <ReasoningTrigger />
        <ReasoningContent>{overrideText}</ReasoningContent>
      </Reasoning>
    );
  } else if (isAgentMessage && mergedEvents && mergedEvents.length > 1) {
    // Merged group without explicit override — use getMergedEventText.
    content = (
      <Message from="assistant" className="max-w-full">
        <MessageContent className="w-full">
          <Markdown>{getMergedEventText(mergedEvents)}</Markdown>
        </MessageContent>
      </Message>
    );
  } else if (allAgentThinking) {
    const mergedText = getMergedEventText(mergedEvents);
    content = (
      <Reasoning defaultOpen>
        <ReasoningTrigger />
        <ReasoningContent>{mergedText}</ReasoningContent>
      </Reasoning>
    );
  } else {
    content = renderEventContent(event, pairedResult, pairedUse, modelErrorCause);
  }

  return (
    <div className="flex w-full min-w-0 flex-col gap-3">
      {onViewInDebug && (
        <div className="flex items-center gap-2 border-b border-border pb-2 text-xs text-muted-foreground">
          <button
            type="button"
            onClick={onViewInDebug}
            className="flex items-center gap-1 hover:text-foreground transition-colors"
          >
            <Link2Icon className="h-3 w-3" />
            <span>View in Debug →</span>
          </button>
          {event.id && (
            <span className="font-mono text-[10px] opacity-60">{event.id}</span>
          )}
          {mergedEvents && mergedEvents.length > 1 && (
            <span className="ml-auto rounded-full bg-green-500/20 px-2 py-0.5 text-[10px] font-medium text-green-700">
              {mergedEvents.length} merged
            </span>
          )}
        </div>
      )}
      <div className="flex min-w-0 w-full flex-col gap-3">
        {content}
      </div>
    </div>
  );
}

function renderToolCard(useEvent: Event, resultEvent?: Event) {
  const mcpServerName =
    useEvent.type === "agent.mcp_tool_use"
      ? (useEvent as { mcp_server_name?: string }).mcp_server_name
      : undefined;
  const baseName = useEvent.name ?? "tool";
  const title = mcpServerName
    ? `${baseName} (mcp · ${mcpServerName})`
    : baseName;

  const rawContent = resultEvent
    ? (resultEvent as { content?: unknown }).content
    : undefined;
  const output: unknown =
    rawContent === undefined
      ? undefined
      : typeof rawContent === "string"
        ? rawContent
        : JSON.stringify(rawContent, null, 2);

  const isError = resultEvent
    ? Boolean((resultEvent as { is_error?: boolean }).is_error)
    : false;
  const errorText = isError
    ? typeof output === "string"
      ? output
      : JSON.stringify(output ?? null)
    : undefined;
  const state = resultEvent
    ? isError
      ? "output-error"
      : "output-available"
    : "input-available";

  // Check for attached files on the result event.
  const files = resultEvent
    ? (resultEvent as { files?: FileAttachment[] }).files
    : undefined;
  const hasFiles = Array.isArray(files) && files.length > 0;

  return (
    <>
      <Tool className="max-w-full">
        <ToolHeader type="dynamic-tool" toolName={title} state={state} />
        <ToolContent>
          <ToolInput input={useEvent.input ?? {}} />
          <ToolOutput
            output={isError ? undefined : output}
            errorText={errorText}
          />
        </ToolContent>
      </Tool>
      {hasFiles && <FileAttachments files={files!} />}
    </>
  );
}

function renderIOSections(
  event: Event,
  pairedResult?: Event,
  pairedUse?: Event,
  modelErrorCause?: { error: string; model?: string }
) {
  const io = getEventIO(event, { pairedResult, pairedUse, modelErrorCause });
  const inputText = formatEventIOValue(io.input);
  const outputText = formatEventIOValue(io.output);

  return (
    <div className="flex w-full min-w-0 flex-col gap-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <div className="font-mono text-xs text-muted-foreground opacity-80">
          {event.type}
        </div>
        {io.unpaired && (
          <span className="rounded bg-warning-subtle px-1.5 py-0.5 text-[10px] font-medium text-warning">
            unpaired
          </span>
        )}
      </div>
      <div>
        <div className="mb-1 text-xs font-medium text-muted-foreground">
          {io.inputLabel ?? "Input"}
        </div>
        {inputText ? (
          <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded bg-muted p-2 text-xs">
            {inputText}
          </pre>
        ) : (
          <div className="text-xs text-muted-foreground opacity-60">—</div>
        )}
      </div>
      <div>
        <div className="mb-1 text-xs font-medium text-muted-foreground">
          {io.outputLabel ?? "Output"}
        </div>
        {outputText ? (
          <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded bg-muted p-2 text-xs">
            {outputText}
          </pre>
        ) : (
          <div className="text-xs text-muted-foreground opacity-60">—</div>
        )}
      </div>
    </div>
  );
}

/**
 * Render the actual event content using ai-elements primitives.
 */
function renderEventContent(
  event: Event,
  pairedResult?: Event,
  pairedUse?: Event,
  modelErrorCause?: { error: string; model?: string }
) {
  switch (event.type) {
    case "user.message": {
      const text = Array.isArray(event.content)
        ? event.content.map((b) => b.text).join("")
        : typeof event.content === "string"
          ? event.content
          : "";
      return (
        <Message from="user" className="ml-0 max-w-full">
          <MessageContent className="w-full">
            <Markdown>{text}</Markdown>
          </MessageContent>
        </Message>
      );
    }

    case "agent.message": {
      const text = (Array.isArray(event.content) ? event.content : [])
        .map((b) => b.text)
        .join("");
      return (
        <Message from="assistant" className="max-w-full">
          <MessageContent className="w-full">
            <Markdown>{text}</Markdown>
          </MessageContent>
        </Message>
      );
    }

    case "agent.thinking": {
      const text = (event as { text?: string }).text ?? "";
      if (!text) return null;
      return (
        <Reasoning isStreaming={false} defaultOpen={false}>
          <ReasoningTrigger />
          <ReasoningContent>{text}</ReasoningContent>
        </Reasoning>
      );
    }

    case "agent.tool_use":
    case "agent.custom_tool_use":
    case "agent.mcp_tool_use":
      return renderToolCard(event, pairedResult);

    case "agent.tool_result":
    case "agent.mcp_tool_result":
    case "user.custom_tool_result": {
      if (pairedUse) {
        const toolCard = renderToolCard(pairedUse, event);
        const files = (event as { files?: FileAttachment[] }).files;
        if (Array.isArray(files) && files.length > 0) {
          return (
            <>
              {toolCard}
              <FileAttachments files={files} />
            </>
          );
        }
        return toolCard;
      }
      const rawContent = (event as { content?: unknown }).content;
      const output: unknown =
        rawContent === undefined
          ? undefined
          : typeof rawContent === "string"
            ? rawContent
            : JSON.stringify(rawContent, null, 2);
      const files = (event as { files?: FileAttachment[] }).files;
      return (
        <Tool className="max-w-full">
          <ToolHeader
            type="dynamic-tool"
            toolName="tool result (unpaired)"
            state="output-available"
          />
          <ToolContent>
            <ToolOutput output={output} errorText={undefined} />
          </ToolContent>
        </Tool>
      );
      // Note: files on unpaired tool_results are rare — Codex always pairs
      // tool_use/tool_result. If they occur, the FileAttachments render is
      // handled in the paired path above.
    }

    case "session.error": {
      const errorText =
        typeof event.error === "string"
          ? event.error
          : event.error
            ? JSON.stringify(event.error)
            : String(event.message ?? "");
      // Prefer actionable upstream cause when present (billing / rate limit).
      const primary =
        modelErrorCause?.error?.trim()
          ? modelErrorCause.error
          : errorText;
      return (
        <div className="w-full rounded-lg bg-danger-subtle px-4 py-2.5 text-sm text-danger">
          <div className="whitespace-pre-wrap break-words">{primary}</div>
          {modelErrorCause && modelErrorCause.error !== primary && (
            <div className="mt-1.5 pt-1.5 text-[12px] opacity-90">
              <span className="font-medium">Cause</span>
              {modelErrorCause.model && (
                <span className="ml-1 font-mono opacity-75">
                  ({modelErrorCause.model})
                </span>
              )}
              : {modelErrorCause.error}
            </div>
          )}
        </div>
      );
    }

    case "session.warning":
      return (
        <div className="w-full bg-warning-subtle rounded-lg px-4 py-2.5 text-sm text-warning">
          <div className="font-medium mb-0.5">
            Warning ({String(event.source ?? "")})
          </div>
          <div>{String(event.message ?? "")}</div>
        </div>
      );

    default:
      return renderIOSections(event, pairedResult, pairedUse, modelErrorCause);
  }
}

/** File metadata attached to agent.tool_result events by the Codex harness. */
export interface FileAttachment {
  filename: string;
  file_id: string;
  media_type: string;
  size_bytes: number;
  download_url: string;
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** Renders a list of file attachments with download links. */
function FileAttachments({ files }: { files: FileAttachment[] }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-lg border border-border bg-muted/30 p-3">
      <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <FileIcon className="h-3.5 w-3.5" />
        <span>{files.length} file{files.length > 1 ? "s" : ""} created</span>
      </div>
      {files.map((file) => (
        <a
          key={file.file_id}
          href={file.download_url}
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors"
        >
          <FileIcon className="h-4 w-4 shrink-0 text-blue-500" />
          <span className="truncate font-medium">{file.filename}</span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {formatFileSize(file.size_bytes)}
          </span>
          <DownloadIcon className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        </a>
      ))}
    </div>
  );
}
