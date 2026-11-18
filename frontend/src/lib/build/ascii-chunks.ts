/**
 * Build-time helpers that keep generated JavaScript ASCII-only.
 *
 * Chromium keeps a script's whole source in memory for lazy compilation, and
 * it stores the source as one byte per character only when every character
 * fits in Latin-1. One translated string outside that range makes it store the
 * entire chunk at two bytes per character, in V8 and again in Blink's resource
 * cache. The entry chunk carries every locale's messages, so a single CJK
 * string doubles the cost of the whole bundle in every open tab. Writing those
 * characters as \uXXXX escapes lets the browser store each chunk at one byte
 * per character.
 */
import { parseAst, type Plugin } from "vite-plus";

const BACKSLASH = 0x5c;

// Part of every chunk hash, because escaping runs after the file names are
// fixed. Change it whenever escapeNonAscii changes, so an escaped chunk never
// reuses the immutable URL of a differently escaped one.
const ESCAPER_VERSION = "ascii-escape-v1";

function unicodeEscape(unit: number): string {
  return "\\u" + unit.toString(16).padStart(4, "0");
}

/**
 * Rewrites every non-ASCII UTF-16 code unit as a \uXXXX escape. In string
 * literals, untagged templates, regular expressions and BMP identifiers the
 * escape means the same character. A non-ASCII character that an odd run of
 * backslashes escapes (an identity escape such as "\é") is written as the
 * bare \uXXXX escape, which has the same value. The rewrite does not know the
 * context of each character, so it can change a program: a line separator
 * that ends a comment or continues a string line, non-ASCII whitespace
 * between tokens, an astral identifier, or the raw text of a tagged
 * template. escapeChunk checks for that.
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

type Node = Record<string, unknown>;

const SKIPPED_KEYS = new Set(["start", "end", "loc", "range", "raw"]);

function normalizePattern(pattern: string): string {
  return pattern
    .replace(/\\u([0-9a-fA-F]{4})/g, (_match, hex: string) =>
      String.fromCharCode(parseInt(hex, 16)),
    )
    .replace(/\\([^\x00-\x7f])/g, "$1");
}

/**
 * Whether two parsed programs mean the same: the same nodes in the same
 * order, the same identifier names, literal values, regular expressions and
 * template text, including the raw text a tag function can read.
 */
function sameProgram(a: Node, b: Node): boolean {
  const stack: [unknown, unknown, boolean][] = [[a, b, false]];
  while (stack.length > 0) {
    const [x, y, tagged] = stack.pop()!;
    if (Array.isArray(x)) {
      if (!Array.isArray(y) || x.length !== y.length) return false;
      for (let i = 0; i < x.length; i++) stack.push([x[i], y[i], tagged]);
      continue;
    }
    if (typeof x !== "object" || x === null || typeof y !== "object" || y === null) {
      if (x !== y) return false;
      continue;
    }
    const nx = x as Node;
    const ny = y as Node;
    if (nx.type !== ny.type) return false;
    if (nx.type === "Literal" && nx.regex) {
      const rx = nx.regex as { pattern: string; flags: string };
      const ry = ny.regex as { pattern: string; flags: string } | undefined;
      if (!ry || rx.flags !== ry.flags) return false;
      if (normalizePattern(rx.pattern) !== normalizePattern(ry.pattern)) return false;
      continue;
    }
    if (nx.type === "TemplateElement") {
      const vx = nx.value as { cooked: string | null; raw: string };
      const vy = ny.value as { cooked: string | null; raw: string };
      if (vx.cooked !== vy.cooked || (tagged && vx.raw !== vy.raw)) return false;
      continue;
    }
    for (const key of Object.keys(nx)) {
      if (SKIPPED_KEYS.has(key)) continue;
      const childTagged =
        nx.type === "TaggedTemplateExpression"
          ? key === "quasi"
          : nx.type === "TemplateLiteral" && tagged && key === "quasis";
      stack.push([nx[key], ny[key], childTagged]);
    }
  }
  return true;
}

function parses(code: string): Node | null {
  try {
    return parseAst(code) as unknown as Node;
  } catch {
    return null;
  }
}

/**
 * The ASCII-only form of a chunk, or null when escaping it would change what
 * the chunk does, as a parse of both versions shows.
 */
export function escapeChunk(code: string): string | null {
  const escaped = escapeNonAscii(code);
  if (escaped === code) return code;
  const before = parses(code);
  const after = parses(escaped);
  if (before === null || after === null || !sameProgram(before, after)) return null;
  return escaped;
}

/**
 * Vite plugin that applies escapeChunk to every emitted chunk. It runs in
 * generateBundle because the minifier runs after renderChunk and would print
 * the escapes back as raw characters. Builds with source maps are left
 * alone, since the escapes would shift the mapped columns.
 */
export function asciiOnlyChunks(): Plugin {
  return {
    name: "agentsview:ascii-only-chunks",
    apply: "build",
    enforce: "post",
    augmentChunkHash() {
      return ESCAPER_VERSION;
    },
    generateBundle(options, bundle) {
      if (options.sourcemap) {
        this.warn("source maps are on, so chunks keep their non-ASCII characters");
        return;
      }
      for (const output of Object.values(bundle)) {
        if (output.type !== "chunk") continue;
        const escaped = escapeChunk(output.code);
        if (escaped === null) {
          this.warn(
            `${output.fileName} keeps its non-ASCII characters: escaping them would change the code`,
          );
          continue;
        }
        output.code = escaped;
      }
    },
  };
}
