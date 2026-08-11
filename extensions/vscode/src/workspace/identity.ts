import { createHash } from "node:crypto";
import { isAbsolute, posix } from "node:path";

import type { TurnStartPayload } from "../protocol/generated.js";

export type WorkspaceIdentity = NonNullable<
TurnStartPayload["workspace_identity"]
>;

const workspaceIdentityVersion = 1;
const maxWorkspaceIdentityBytes = 4096;

export function createWorkspaceIdentity(
  editorURI: string,
  runtimePath: string,
): WorkspaceIdentity {
  if (Buffer.byteLength(editorURI, "utf8") === 0 ||
    Buffer.byteLength(editorURI, "utf8") > maxWorkspaceIdentityBytes ||
    Buffer.byteLength(runtimePath, "utf8") === 0 ||
    Buffer.byteLength(runtimePath, "utf8") > maxWorkspaceIdentityBytes ||
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
  if (parsed.protocol !== "file:" || parsed.host !== "") {
    throw new Error("workspace identity requires a local file URI");
  }
  return {
    version: workspaceIdentityVersion,
    root_id: createHash("sha256").update(editorURI).digest("hex"),
    editor_uri: editorURI,
    runtime_path: runtimePath,
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
