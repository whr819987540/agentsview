import { describe, expect, it } from "vite-plus/test";
import { escapeChunk, escapeNonAscii } from "./ascii-chunks.js";

// Evaluate a function body both ways and compare what it returns.
function run(code: string): unknown {
  return new Function(code)();
}

function expectSameResult(code: string) {
  const escaped = escapeNonAscii(code);
  expect(escaped).toMatch(/^[\x00-\x7f]*$/);
  expect(run(escaped)).toEqual(run(code));
}

describe("escapeNonAscii", () => {
  it("leaves ASCII-only code untouched", () => {
    const code = 'return ["a", `b${1}`, /c+/.source];';
    expect(escapeNonAscii(code)).toBe(code);
  });

  it("keeps string and template literal values", () => {
    expectSameResult('return ["会话", \'Sessão\', `代理 ${"é"} 세션`];');
  });

  it("keeps regular expression matches", () => {
    expectSameResult('return ["中文测试".match(/[一-龥]+/)[0], /é/i.test("É"), /😀/u.test("😀")];');
  });

  it("keeps astral characters as one code point", () => {
    expectSameResult('const s = "😀"; return [s.length, s.codePointAt(0), [..."a😀b"].length];');
  });

  it("keeps identifiers that use non-ASCII letters", () => {
    expectSameResult("const café = 1; const 名前 = café + 1; return 名前;");
  });

  it("drops the backslash of an identity escape", () => {
    expectSameResult('return ["\\é", `\\会`, /\\é/.test("é")];');
  });

  it("does not treat an escaped backslash as an escape introducer", () => {
    expectSameResult('return ["\\\\é", "\\\\\\é"];');
  });

  it("escapes line separators inside strings", () => {
    const code = 'return "a b c";';
    expect(escapeNonAscii(code)).toBe('return "a\\u2028b\\u2029c";');
    expectSameResult(code);
  });
});

describe("escapeChunk", () => {
  it("escapes a module whose strings, templates and patterns survive the rewrite", () => {
    const code = 'export const s = ["会话", `代理 ${1}`, /é+/u.source, { "名": 1 }];\n';
    const escaped = escapeChunk(code);
    expect(escaped).not.toBeNull();
    expect(escaped).toMatch(/^[\x00-\x7f]*$/);
  });

  it("returns ASCII-only code unchanged", () => {
    const code = "export const a = 1;\n";
    expect(escapeChunk(code)).toBe(code);
  });

  it("keeps a chunk whose line comment ends at a line separator", () => {
    // The separator ends the comment, so the assignment after it runs.
    expect(escapeChunk("let x = 1; // note x = 2;\nexport default x;\n")).toBeNull();
  });

  it("keeps a chunk that continues a string line with a line separator", () => {
    expect(escapeChunk('export const s = "a\\ b";\n')).toBeNull();
  });

  it("keeps a chunk with non-ASCII whitespace between tokens", () => {
    expect(escapeChunk("export const a = 1;\n")).toBeNull();
  });

  it("keeps a chunk with an astral identifier", () => {
    expect(escapeChunk("export const o = { \u{1d4b3}: 1 };\n")).toBeNull();
  });

  it("keeps a chunk whose tagged template reads non-ASCII raw text", () => {
    expect(escapeChunk("export const r = String.raw`é`;\n")).toBeNull();
  });
});
