import { createHash } from "node:crypto";
import assert from "node:assert/strict";
import test from "node:test";

import { canonicalEditorURI } from "./uri.js";

void test("canonical editor URI keeps remote authority raw and path encoded", () => {
  assert.equal(canonicalEditorURI({
    scheme: "vscode-remote",
    authority: "ssh-remote+build-host",
    toString: () =>
      "vscode-remote://ssh-remote%2Bbuild-host/workspace/value%20one.ts",
  }), "vscode-remote://ssh-remote+build-host/workspace/value%20one.ts");
  assert.equal(canonicalEditorURI({
    scheme: "file",
    authority: "",
    toString: () => "file:///workspace/value%20one.ts",
  }), "file:///workspace/value%20one.ts");
  const hostID = createHash("sha256")
    .update("remote-host")
    .digest("hex")
    .slice(0, 16);
  assert.equal(canonicalEditorURI({
    scheme: "file",
    authority: "",
    toString: () => "file:///workspace/value%20one.ts",
  }, "ssh-remote", "remote-host"), "vscode-remote://ssh-remote+" +
    `${hostID}/workspace/value%20one.ts`);
});

void test("canonical editor URI rejects forged remote authority", () => {
  assert.throws(() => canonicalEditorURI({
    scheme: "vscode-remote",
    authority: "ssh-remote+host/escape",
    toString: () => "vscode-remote://ssh-remote%2Bhost%2Fescape/workspace",
  }), /authority/u);
});
