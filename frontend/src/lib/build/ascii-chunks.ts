/**
 * Build-time helpers that keep generated JavaScript ASCII-only.
 *
 * Chromium keeps a script's whole source in memory for lazy compilation, and
 * it stores the source as one byte per character only when every character
 * fits in Latin-1. One translated string outside that range makes it store the
 * entire chunk at two bytes per character, in V8 and again in Blink's resource
 * cache. The entry chunk carries every locale's messages, so a single CJK
 * string doubles the cost of the whole bundle in every open tab. Writing those
 * characters as \uXXXX escapes keeps the text identical at runtime and lets the
 * browser store each chunk at one byte per character.
 */
import type { Plugin } from "vite-plus";

const BACKSLASH = 0x5c;

function unicodeEscape(unit: number): string {
  return "\\u" + unit.toString(16).padStart(4, "0");
}

/**
 * Rewrites every non-ASCII UTF-16 code unit of generated JavaScript as a
 * \uXXXX escape, which means the same character in string, template and
 * regular-expression literals, in identifiers, and in comments. A non-ASCII
 * character that an odd run of backslashes escapes (an identity escape such
 * as "\é") is written as the bare \uXXXX escape, which has the same value.
 * Astral characters become two escaped surrogates, which every literal form
 * reads back as the same code point.
 */
export function escapeNonAscii(code: string): string {
  let out = "";
  let start = 0;
  for (let i = 0; i < code.length; i++) {
    const unit = code.charCodeAt(i);
    if (unit < 0x80) continue;
    let slashes = 0;
    for (let j = i - 1; j >= 0 && code.charCodeAt(j) === BACKSLASH; j--) slashes++;
    out += code.slice(start, slashes % 2 === 1 ? i - 1 : i) + unicodeEscape(unit);
    start = i + 1;
  }
  return start === 0 ? code : out + code.slice(start);
}

/**
 * Vite plugin that applies escapeNonAscii to every emitted chunk. It runs in
 * generateBundle because the minifier runs after renderChunk and would print
 * the escapes back as raw characters.
 */
export function asciiOnlyChunks(): Plugin {
  return {
    name: "agentsview:ascii-only-chunks",
    apply: "build",
    enforce: "post",
    generateBundle(_options, bundle) {
      for (const output of Object.values(bundle)) {
        if (output.type === "chunk") output.code = escapeNonAscii(output.code);
      }
    },
  };
}
