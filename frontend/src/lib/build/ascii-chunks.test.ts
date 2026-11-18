import { describe, expect, it } from "vite-plus/test";
import { escapeNonAscii } from "./ascii-chunks.js";

// Evaluate a module body both ways and compare what it returns.
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

  it("keeps comments harmless", () => {
    expectSameResult("// 注释 — comment\n/* 多行 */ return 1;");
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
