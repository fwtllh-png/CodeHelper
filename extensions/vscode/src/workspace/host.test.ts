import assert from "node:assert/strict";
import test from "node:test";

import {
  assertWorkspaceExtensionHost,
  authorityMatchesRemoteName,
} from "./host.js";

void test("Workspace Extension Host accepts local and remote file storage", () => {
  assert.doesNotThrow(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "file",
      workspaceAuthority: "",
      storageScheme: "file",
    });
  });
  assert.doesNotThrow(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "file",
      workspaceAuthority: "",
      storageScheme: "file",
      remoteName: "ssh-remote",
    });
  });
  assert.doesNotThrow(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "vscode-remote",
      workspaceAuthority: "ssh-remote+build-host",
      storageScheme: "file",
      remoteName: "ssh-remote",
    });
  });
  assert.equal(
    authorityMatchesRemoteName("dev-container+sha256", "dev-container"),
    true,
  );
});

void test("Workspace Extension Host rejects UI-side and forged remote state", () => {
  assert.throws(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "vscode-remote",
      workspaceAuthority: "ssh-remote+build-host",
      storageScheme: "file",
    });
  }, /local Workspace Extension Host/u);
  assert.throws(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "vscode-remote",
      workspaceAuthority: "dev-container+forged",
      storageScheme: "file",
      remoteName: "ssh-remote",
    });
  }, /authority/u);
  assert.throws(() => {
    assertWorkspaceExtensionHost({
      workspaceScheme: "vscode-remote",
      workspaceAuthority: "ssh-remote+build-host",
      storageScheme: "vscode-userdata",
      remoteName: "ssh-remote",
    });
  }, /file storage/u);
});
