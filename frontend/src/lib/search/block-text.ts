/** Collect the searchable text blocks rendered by one transcript message. */
import type { DbMessage as Message, DbToolCall as ToolCall } from "../api/generated/index.js";
import { detachedString, LRUCache } from "../utils/cache.js";
import { enrichSegments, parseContent } from "../utils/content-parser.js";
import { renderMarkdown, type MarkdownRenderOptions } from "../utils/markdown.js";
import { isSystemMessage } from "../utils/messages.js";
import { displayToolResult } from "../utils/toolDisplay.js";
import { domText } from "./dom-text.js";
import { resolveToolInputText } from "./tool-input.js";

export { resolveToolInputText } from "./tool-input.js";

export type SearchBlockKind =
  | "text"
  | "thinking"
  | "code"
  | "skill"
  | "tool-input"
  | "tool-output"
  | "tool-history";

export interface SearchBlock {
  key: string;
  ordinal: number;
  kind: SearchBlockKind;
  text: string;
  label?: string;
}

let messageBlocks = [new WeakMap<Message, SearchBlock[]>(), new WeakMap<Message, SearchBlock[]>()];
// Rendered plain text by source text, bounded by entry count and by about
// a million UTF-16 code units of source and text per render mode.
const TEXT_CACHE_MAX_UNITS = 1_000_000;
const textBudget = {
  maxSize: TEXT_CACHE_MAX_UNITS,
  sizeOf: (content: string, text: string) => content.length + text.length,
};
const markdownText = [
  new LRUCache<string, string>(500, textBudget),
  new LRUCache<string, string>(500, textBudget),
];

/**
 * Forget the search blocks built for the loaded messages. Find builds them
 * for every message it searches, with rendered and folded copies of their
 * text, so closing find releases them; anything that needs blocks again
 * rebuilds them on demand.
 */
export function releaseSearchBlocks(): void {
  messageBlocks = [new WeakMap(), new WeakMap()];
}

/** Keys are independent of filters, mount state, and search query. */
export function blockKey(ordinal: number, kind: SearchBlockKind, index: number | string): string {
  return `${ordinal}:${kind}:${index}`;
}

function renderedText(content: string, options: MarkdownRenderOptions): string {
  const cache = markdownText[options.renderUnknownXmlBlocksAsPreformatted ? 1 : 0]!;
  const cached = cache.get(content);
  if (cached !== undefined) return cached;
  const root = new DOMParser().parseFromString(renderMarkdown(content, options), "text/html");
  const text = domText(root.body);
  cache.set(detachedString(content), text);
  return text;
}

/**
 * Mirror MessageContent's segment-first, tool-call-second rendering order.
 * Message object identity is the version: SSE replacements invalidate this cache.
 * Inline subagent transcripts belong to another session and are not traversed.
 */
export function collectSearchBlocks(
  message: Message,
  options: MarkdownRenderOptions = {},
): SearchBlock[] {
  const cache = messageBlocks[options.renderUnknownXmlBlocksAsPreformatted ? 1 : 0]!;
  const cached = cache.get(message);
  if (cached) return cached;
  const blocks: SearchBlock[] = [];
  cache.set(message, blocks);
  if (
    isSystemMessage(message) ||
    message.is_compact_boundary ||
    (message.is_system && message.source_subtype && message.source_subtype !== "compact_boundary")
  )
    return blocks;

  const segments = enrichSegments(
    parseContent(message.content, message.has_tool_use, message.id, message.content_length),
    message.tool_calls,
  );
  const add = (kind: SearchBlockKind, index: number | string, text: string, label?: string) => {
    if (!text) return;
    blocks.push({
      key: blockKey(message.ordinal, kind, index),
      ordinal: message.ordinal,
      kind,
      text,
      label,
    });
  };
  segments.forEach((segment, index) => {
    if (segment.type === "tool") return;
    const text =
      segment.type === "text" || segment.type === "skill"
        ? renderedText(segment.content, options)
        : segment.content;
    add(segment.type, index, text, segment.label);
  });

  const addTool = (
    call: ToolCall | undefined,
    content: string,
    index: number | string,
    label?: string,
  ) => {
    const name = call?.tool_name ?? label;
    add("tool-input", index, resolveToolInputText(call, content), name);
    add("tool-output", index, displayToolResult(call?.result_content ?? ""), name);
    call?.result_events?.forEach((event, eventIndex) => {
      add("tool-history", `${index}.${eventIndex}`, displayToolResult(event.content), name);
    });
  };
  if (message.tool_calls?.length) {
    message.tool_calls.forEach((call, index) => addTool(call, "", index));
  } else {
    segments
      .filter((segment) => segment.type === "tool")
      .forEach((segment, index) => {
        addTool(segment.toolCall, segment.content, `seg${index}`, segment.label);
      });
  }
  return blocks;
}
