import assert from "node:assert/strict";
import test from "node:test";

import { parseContextDirectives } from "./directives.js";

void test("parseContextDirectives extracts only explicit context tokens", () => {
  const parsed = parseContextDirectives(
    "Review @file, explain @selection, inspect @symbol and triage @diagnostics.",
  );
  assert.equal(parsed.prompt, "Review, explain, inspect and triage.");
  assert.deepEqual(
    [...parsed.directives].sort(),
    ["diagnostics", "file", "selection", "symbol"],
  );
});

void test("parseContextDirectives does not interpret paths or identifiers", () => {
  const parsed = parseContextDirectives("Keep @filename and user@file.example");
  assert.equal(parsed.prompt, "Keep @filename and user@file.example");
  assert.equal(parsed.directives.size, 0);
});
