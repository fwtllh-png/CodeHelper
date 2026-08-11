import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { hostLocalStoragePath } from "./host-storage.js";

void test("hostLocalStoragePath accepts local file and vscode-userdata storage", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-host-storage-"));
  try {
    for (const scheme of ["file", "vscode-userdata"]) {
      const path = await hostLocalStoragePath({
        uiExtensionHost: true,
        remoteName: undefined,
        scheme,
        fsPath: join(root, scheme),
      });
      assert.equal(path, join(root, scheme));
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("hostLocalStoragePath rejects remote and virtual storage", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-host-storage-"));
  try {
    await assert.rejects(hostLocalStoragePath({
      uiExtensionHost: true,
      remoteName: "ssh-remote+host",
      scheme: "file",
      fsPath: root,
    }), /local UI Extension Host/u);
    await assert.rejects(hostLocalStoragePath({
      uiExtensionHost: true,
      remoteName: undefined,
      scheme: "memfs",
      fsPath: root,
    }), /not Host-local/u);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("hostLocalStoragePath rejects a non-directory path", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-host-storage-"));
  const path = join(root, "file");
  try {
    await writeFile(path, "not a directory", "utf8");
    await assert.rejects(hostLocalStoragePath({
      uiExtensionHost: true,
      remoteName: undefined,
      scheme: "file",
      fsPath: path,
    }));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
