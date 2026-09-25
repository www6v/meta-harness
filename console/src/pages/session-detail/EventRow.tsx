/**
 * EventRow — compact sidebar row for event lists.
 *
 * Used by both TranscriptTab (turn list) and DebugTab (flat event list).
 * Shows: icon + text snippet + timestamp + metadata badge.
 *
 * This is the "summary" view — click to expand into EventDetail.
 */

import { cn } from "@/lib/utils";
import {
  AlertCircleIcon,
  BotIcon,
  ClockIcon,
  FileIcon,
  InfoIcon,
  MessageSquareIcon,
  UserIcon,
  WrenchIcon,
} from "lucide-react";
import type { Event } from "../../lib/events";
import { formatRelative } from "../../lib/format";

export interface EventRowProps {
  event: Event;
  selected?: boolean;
  onClick?: () => void;
  className?: string;
  /**
   * If this row represents a group of merged consecutive events,
   * this is the total number of events in the group.
   */
  mergedCount?: number;
  /**
   * Combined text snippet from all merged events. When present,
   * overrides the single-event snippet for the row label.
   */
  mergedText?: string;
}

/**
 * Get the icon for an event type.
 */
function getEventIcon(type: string) {
  switch (type) {
    case "user.message":
    case "user/message":
      return UserIcon;
    case "agent.message":
    case "assistant/message":
      return MessageSquareIcon;
    case "agent.thinking":
      return InfoIcon;
    case "agent.tool_use":
    case "agent.custom_tool_use":
    case "agent.mcp_tool_use":
    case "tool/call":
      return WrenchIcon;
    case "session.error":
    case "session.warning":
    case "assistant/attempt":
      return AlertCircleIcon;
    default:
      return BotIcon;
  }
}

/**
 * Get the category for transcript filtering.
 *
 * DeepSeek harness event types mapping:
 * - Message events (消息事件): user/message, system/message, assistant/message, tool/result
 * - Tool events (工具事件): tool/call, tool/result
 * - Auxiliary events (辅助事件): assistant/attempt
 */
export type TranscriptCategory = "user" | "agent" | "tool" | "error" | "system" | "message" | "auxiliary";

export function categorizeEvent(event: Event): TranscriptCategory {
  const type = event.type;

  // DeepSeek harness events - Message events (消息事件)
  if (type === "user/message" || type === "user.message") {
    return "user";
  }
  if (type === "system/message") {
    return "message";
  }
  if (type === "assistant/message") {
    return "message";
  }

  // DeepSeek harness events - Tool events (工具事件)
  if (type === "tool/call") {
    return "tool";
  }
  if (type === "tool/result") {
    return "tool";
  }

  // DeepSeek harness events - Auxiliary events (辅助事件)
  if (type === "assistant/attempt") {
    return "auxiliary";
  }

  // Standard OMA events
  switch (type) {
    case "user.message":
      return "user";
    case "agent.message":
    case "agent.thinking":
      return "agent";
    case "agent.tool_use":
    case "agent.custom_tool_use":
    case "agent.mcp_tool_use":
    case "agent.tool_result":
    case "agent.mcp_tool_result":
    case "user.custom_tool_result":
      return "tool";
    case "session.error":
    case "session.warning":
      return "error";
    default:
      return "system";
  }
}

/**
 * A display event can be a single event or a merged group of consecutive
 * agent events (for compact display). Shared between TranscriptTab and
 * DebugTab — both want the same "collapse adjacent agent.message events"
 * behavior.
 */
export type DisplayEvent = {
  events: Event[];
  category: TranscriptCategory;
  /** Primary event for selection/detail (first in group) */
  primaryEvent: Event;
};

/**
 * Merge consecutive agent.message events into a single display event.
 * Thinking / tool_use / other categories break the run.
 *
 * Cumulative streaming fix: harnesses often emit multiple `agent.message`
 * events sharing the same `message_id` (increasing `seq`, growing content).
 * Without dedup, every cumulative update becomes a separate row — each
 * showing the full text, causing "大量重复的内容".  We deduplicate by
 * `message_id`: keep only the last (most complete) event per id.
 */
export function mergeConsecutiveAgentEvents(events: Event[]): DisplayEvent[] {
  const result: DisplayEvent[] = [];
  let currentGroup: Event[] = [];
  let currentType: string | null = null;

  for (const e of events) {
    const category = categorizeEvent(e);

    // Group consecutive events of the same type within the agent category.
    // Both agent.message and agent.thinking use cumulative streaming (same
    // `id`, increasing `seq`, growing content). Without merging, every
    // cumulative update renders as a separate row — "大量重复的内容".
    // We keep thinking and message in separate groups so the "Thinking:"
    // prefix stays distinct from the final reply.
    const isMergeable =
      category === "agent"
      && (e.type === "agent.message" || e.type === "agent.thinking");

    if (isMergeable && e.type === currentType) {
      // Deduplicate by message_id: if this event shares a message_id with
      // an earlier event in the group, replace it (keep the latest text).
      const mid = (e as { message_id?: unknown }).message_id;
      if (typeof mid === "string" && mid.length > 0) {
        const dupIdx = currentGroup.findIndex(
          (ge) => (ge as { message_id?: unknown }).message_id === mid
        );
        if (dupIdx >= 0) {
          // Replace the older duplicate with this newer, more complete event.
          currentGroup[dupIdx] = e;
          // Don't push — we replaced in-place.
          continue;
        }
      }
      currentGroup.push(e);
    } else {
      if (currentGroup.length > 0) {
        result.push({
          events: [...currentGroup],
          category: "agent",
          primaryEvent: currentGroup[0],
        });
        currentGroup = [];
      }
      if (isMergeable) {
        currentGroup = [e];
        currentType = e.type;
      } else {
        currentType = null;
        result.push({ events: [e], category, primaryEvent: e });
      }
    }
  }

  if (currentGroup.length > 0) {
    result.push({
      events: [...currentGroup],
      category: "agent",
      primaryEvent: currentGroup[0],
    });
  }

  return result;
}

/**
 * Combine text from merged consecutive agent.message events.
 *
 * Two streaming modes exist:
 * 1. **Cumulative** (DeepSeek / OpenClaw / Hermes): each event contains the
 *    full text accumulated so far. Each event's text is a prefix of the next.
 *    → Use only the last event (it has the complete message).
 * 2. **Delta** (standard harness): each event is an independent fragment.
 *    → Concatenate all fragments.
 *
 * Detection: if every event's text is a prefix of the next event's text,
 * it's cumulative streaming.
 */
export function getMergedEventText(events: Event[]): string {
  if (events.length <= 1) {
    return events.length === 1 ? extractEventText(events[0]) : "";
  }

  const texts = events.map((e) => extractEventText(e)).filter((t) => t.length > 0);
  if (texts.length <= 1) return texts[0] ?? "";

  // Detect cumulative streaming: check if each text is a prefix of the next.
  // We only need to check a few pairs to be confident.
  const checkLimit = Math.min(3, texts.length - 1);
  let isCumulative = true;
  for (let i = 0; i < checkLimit; i++) {
    if (!texts[i + 1].startsWith(texts[i])) {
      isCumulative = false;
      break;
    }
  }

  if (isCumulative) {
    // Cumulative streaming — last event has the complete text.
    return texts[texts.length - 1];
  }

  // Not cumulative — delta streaming: each event is an independent fragment.
  // Concatenate all fragments to reconstruct the full message.
  // This is the case for the DeepSeek / Codex harness where each
  // `assistant/chunk` produces a separate `agent.message` event with
  // only the new text fragment.
  return texts.join("");
}

function extractEventText(e: Event): string {
  if (Array.isArray(e.content)) {
    return e.content.map((b) => b.text).join("");
  }
  if (typeof e.content === "string") {
    return e.content;
  }
  // agent.thinking stores its text on `e.text` rather than `e.content`.
  // Without this fallback, thinking events with only `e.text` return ""
  // and get filtered out of getMergedEventText's concatenation — the
  // merged row's text would be wrong for cumulative thinking streams.
  if (typeof (e as { text?: unknown }).text === "string") {
    return (e as { text: string }).text;
  }
  return "";
}

/**
 * Get a text snippet from an event (first ~50 chars).
 */
function getEventSnippet(event: Event): string {
  const type = event.type;

  // DeepSeek harness events
  if (type === "user/message" || type === "user.message") {
    const text = extractEventText(event);
    return text.slice(0, 50) + (text.length > 50 ? "…" : "");
  }

  if (type === "assistant/message" || type === "agent.message") {
    const text = extractEventText(event);
    return text.slice(0, 50) + (text.length > 50 ? "…" : "");
  }

  if (type === "system/message") {
    const text = extractEventText(event);
    return `System: ${text.slice(0, 40)}${text.length > 40 ? "…" : ""}`;
  }

  if (type === "tool/call") {
    const name = event.name ?? "tool";
    return `Calling ${name}()`;
  }

  if (type === "tool/result") {
    return "Tool result";
  }

  if (type === "assistant/attempt") {
    return "Assistant attempt (no surface message)";
  }

  // Standard OMA events
  switch (type) {
    case "user.message": {
      const text = extractEventText(event);
      return text.slice(0, 50) + (text.length > 50 ? "…" : "");
    }
    case "agent.message": {
      const text = extractEventText(event);
      return text.slice(0, 50) + (text.length > 50 ? "…" : "");
    }
    case "agent.thinking": {
      const text =
        typeof (event as { text?: unknown }).text === "string"
          ? (event as { text: string }).text
          : extractEventText(event);
      return `Thinking: ${text.slice(0, 40)}${text.length > 40 ? "…" : ""}`;
    }
    case "agent.tool_use":
    case "agent.custom_tool_use":
    case "agent.mcp_tool_use": {
      const name = event.name ?? "tool";
      return `${name}()`;
    }
    case "agent.tool_result":
    case "agent.mcp_tool_result": {
      // Show file count if the tool result has attached files.
      const files = (event as { files?: unknown[] }).files;
      if (Array.isArray(files) && files.length > 0) {
        return `${files.length} file${files.length > 1 ? "s" : ""} created`;
      }
      return "Tool result";
    }
    case "session.error": {
      return event.message ?? event.error ?? "Error";
    }
    case "session.warning": {
      return event.message ?? "Warning";
    }
    default: {
      return type;
    }
  }
}

/**
 * Get a metadata badge for an event.
 */
function getEventBadge(event: Event): string | null {
  switch (event.type) {
    case "agent.tool_use":
    case "agent.custom_tool_use":
      return event.name ?? null;
    case "agent.mcp_tool_use": {
      const server = event.mcp_server_name;
      return server ? `mcp · ${server}` : event.name ?? null;
    }
    case "agent.tool_result":
    case "agent.mcp_tool_result": {
      const files = (event as { files?: unknown[] }).files;
      if (Array.isArray(files) && files.length > 0) {
        return `📎 ${files.length} file${files.length > 1 ? "s" : ""}`;
      }
      return null;
    }
    case "session.error": {
      const model = (event as { model?: string }).model;
      return model ?? null;
    }
    default:
      return null;
  }
}

/**
 * Get timestamp string for an event.
 */
function getEventTimestamp(event: Event): string | null {
  // Prefer processed_at (ISO ms), fall back to ts (unix seconds)
  const pa = (event.data as { processed_at?: string } | undefined)?.processed_at
    ?? (event as { processed_at?: string }).processed_at;
  if (typeof pa === "string") {
    const t = Date.parse(pa);
    if (Number.isFinite(t)) return formatRelative(Date.now() - t);
  }
  if (typeof event.ts === "number") {
    return formatRelative(Date.now() - event.ts * 1000);
  }
  return null;
}

export function EventRow({
  event,
  selected,
  onClick,
  className,
  mergedCount,
  mergedText,
}: EventRowProps) {
  const Icon = getEventIcon(event.type);
  const rawSnippet = getEventSnippet(event);
  // If merged, use combined text (first ~80 chars) instead of single-event snippet
  const snippet =
    mergedText !== undefined
      ? mergedText.slice(0, 80) + (mergedText.length > 80 ? "…" : "")
      : rawSnippet;
  const badge = getEventBadge(event);
  const timestamp = getEventTimestamp(event);
  const category = categorizeEvent(event);
  const isMerged = (mergedCount ?? 0) > 1;

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors",
        "hover:bg-accent/50",
        selected && "bg-accent",
        className
      )}
    >
      <Icon
        className={cn(
          "mt-0.5 h-4 w-4 shrink-0",
          category === "user" && "text-blue-500",
          category === "agent" && "text-green-500",
          category === "tool" && "text-orange-500",
          category === "error" && "text-red-500",
          category === "system" && "text-gray-500"
        )}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex items-center gap-1.5">
          <span className="truncate font-medium">{snippet}</span>
          {isMerged && (
            <span className="shrink-0 rounded-full bg-green-500/20 px-1.5 py-0 text-[10px] font-medium text-green-700">
              ×{mergedCount}
            </span>
          )}
          {badge && (
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
              {badge}
            </span>
          )}
        </div>
        {timestamp && (
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <ClockIcon className="h-3 w-3" />
            <span>{timestamp}</span>
          </div>
        )}
      </div>
    </button>
  );
}
