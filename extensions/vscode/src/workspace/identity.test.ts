import assert from "node:assert/strict";
import test from "node:test";

import { createWorkspaceIdentity } from "./identity.js";

void test("workspace identity distinguishes local and remote editor roots", () => {
  const local = createWorkspaceIdentity("file:///workspace", "/workspace");
  const remote = createWorkspaceIdentity(
    "vscode-remote://ssh-remote+dev/workspace",
    "/workspace",
    "ssh-remote",
  );
  assert.equal(local.version, 1);
  assert.equal(local.root_id.length, 64);
  assert.equal(remote.remote_name, "ssh-remote");
  assert.notEqual(remote.root_id, local.root_id);
});

void test("workspace identity rejects forged URI and runtime fields", () => {
  assert.throws(
    () => createWorkspaceIdentity("file:///workspace?forged=true", "/workspace"),
    /canonical/,
  );
  assert.throws(
    () => createWorkspaceIdentity(
      "vscode-remote://dev-container+forged/workspace",
      "/workspace",
      "ssh-remote",
    ),
    /authority/u,
  );
  assert.throws(
    () => createWorkspaceIdentity("file:///workspace", "relative"),
    /fields/,
  );
  assert.throws(
    () => createWorkspaceIdentity(
      "vscode-remote://ssh-remote+dev/workspace",
      "/workspace",
    ),
    /authority/,
  );
  assert.throws(
    () => createWorkspaceIdentity(
      "https://example.com/workspace",
      "/workspace",
      "ssh-remote",
    ),
    /unsupported/,
  );
});
