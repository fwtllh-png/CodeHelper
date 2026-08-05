import { createHash } from "node:crypto";
import { isAbsolute, posix } from "node:path";

import type { TurnStartPayload } from "../protocol/generated.js";
import { authorityMatchesRemoteName } from "./host.js";

export type WorkspaceIdentity = NonNullable<
TurnStartPayload["workspace_identity"]
>;

const workspaceIdentityVersion = 1;
const maxWorkspaceIdentityBytes = 4096;
const maxRemoteNameBytes = 128;

export function createWorkspaceIdentity(
  editorURI: string,
  runtimePath: string,
  remoteName?: string,
): WorkspaceIdentity {
  if (Buffer.byteLength(editorURI, "utf8") === 0 ||
    Buffer.byteLength(editorURI, "utf8") > maxWorkspaceIdentityBytes ||
    Buffer.byteLength(runtimePath, "utf8") === 0 ||
    Buffer.byteLength(runtimePath, "utf8") > maxWorkspaceIdentityBytes ||
    Buffer.byteLength(remoteName ?? "", "utf8") > maxRemoteNameBytes ||
    !isAbsolute(runtimePath)) {
    throw new Error("workspace identity fields are invalid");
  }
  let parsed: URL;
  try {
    parsed = new URL(editorURI);
  } catch {
    throw new Error("workspace editor URI is invalid");
  }
  if (parsed.toString() !== editorURI ||
    parsed.username !== "" || parsed.password !== "" ||
    parsed.search !== "" || parsed.hash !== "" ||
    parsed.pathname === "" || !parsed.pathname.startsWith("/") ||
    posix.normalize(parsed.pathname) !== parsed.pathname ||
    !canonicalPercentEscapes(editorURI)) {
    throw new Error("workspace editor URI is not canonical");
  }
  if (parsed.protocol === "file:") {
    if (parsed.host !== "" || remoteName !== undefined) {
      throw new Error("local workspace identity cannot carry remote authority");
    }
  } else if (parsed.protocol === "vscode-remote:") {
    if (parsed.host === "" || remoteName === undefined ||
      !/^[A-Za-z0-9._-]+$/u.test(remoteName) ||
      !authorityMatchesRemoteName(parsed.host, remoteName)) {
      throw new Error("remote workspace identity requires authority and remote name");
    }
  } else {
    throw new Error("workspace editor URI scheme is unsupported");
  }
  return {
    version: workspaceIdentityVersion,
    root_id: createHash("sha256").update(editorURI).digest("hex"),
    editor_uri: editorURI,
    runtime_path: runtimePath,
    ...(remoteName === undefined ? {} : { remote_name: remoteName }),
  };
}

function canonicalPercentEscapes(value: string): boolean {
  for (let index = 0; index < value.length; index++) {
    if (value[index] !== "%") {
      continue;
    }
    const escape = value.slice(index + 1, index + 3);
    if (!/^[0-9A-F]{2}$/u.test(escape)) {
      return false;
    }
    index += 2;
  }
  return true;
}
