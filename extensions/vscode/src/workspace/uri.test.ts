import assert from "node:assert/strict";
import test from "node:test";

import { canonicalEditorURI } from "./uri.js";

void test("canonical editor URI accepts encoded local file paths", () => {
  assert.equal(canonicalEditorURI({
    scheme: "file",
    authority: "",
    toString: () => "file:///workspace/value%20one.ts",
  }), "file:///workspace/value%20one.ts");
});

void test("canonical editor URI rejects remote and authoritative file URIs", () => {
  assert.throws(() => canonicalEditorURI({
    scheme: "vscode-remote",
    authority: "ssh-remote+build-host",
    toString: () => "vscode-remote://ssh-remote%2Bbuild-host/workspace",
  }), /only local file URIs/u);
  assert.throws(() => canonicalEditorURI({
    scheme: "file",
    authority: "remote-host",
    toString: () => "file://remote-host/workspace",
  }), /only local file URIs/u);
});
