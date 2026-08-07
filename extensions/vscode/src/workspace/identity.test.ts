import assert from "node:assert/strict";
import test from "node:test";

import { createWorkspaceIdentity } from "./identity.js";

void test("workspace identity binds a canonical local editor root", () => {
  const local = createWorkspaceIdentity("file:///workspace", "/workspace");
  assert.equal(local.version, 1);
  assert.equal(local.root_id.length, 64);
  assert.equal(local.remote_name, undefined);
});

void test("workspace identity rejects remote, forged, and invalid fields", () => {
  assert.throws(
    () => createWorkspaceIdentity("file:///workspace?forged=true", "/workspace"),
    /canonical/u,
  );
  assert.throws(
    () => createWorkspaceIdentity(
      "vscode-remote://ssh-remote+dev/workspace",
      "/workspace",
    ),
    /local file URI/u,
  );
  assert.throws(
    () => createWorkspaceIdentity("file:///workspace", "relative"),
    /fields/u,
  );
  assert.throws(
    () => createWorkspaceIdentity("https://example.com/workspace", "/workspace"),
    /local file URI/u,
  );
});
