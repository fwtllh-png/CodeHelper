import assert from "node:assert/strict";
import test from "node:test";

import { assertWorkspaceExtensionHost } from "./host.js";

void test("UI Extension Host accepts only local file workspaces", () => {
  assert.doesNotThrow(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "file",
      workspaceAuthority: "",
      storageScheme: "file",
    });
  });
  assert.throws(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "vscode-remote",
      workspaceAuthority: "ssh-remote+build-host",
      storageScheme: "file",
    });
  }, /only local file workspaces/u);
  assert.throws(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "file",
      workspaceAuthority: "remote-host",
      storageScheme: "file",
    });
  }, /only local file workspaces/u);
  assert.throws(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "file",
      workspaceAuthority: "",
      storageScheme: "vscode-userdata",
    });
  }, /file storage/u);
});
