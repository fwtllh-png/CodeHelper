import { createHash } from "node:crypto";
import { hostname } from "node:os";

export interface EditorURI {
  readonly scheme: string;
  readonly authority: string;
  toString(): string;
}

export function canonicalEditorURI(
  uri: EditorURI,
  remoteName?: string,
  remoteHostname = hostname(),
): string {
  const encoded = uri.toString();
  if (remoteName !== undefined && uri.scheme === "file") {
    if (uri.authority !== "" ||
      !/^[A-Za-z0-9._-]+$/u.test(remoteName) ||
      remoteHostname.length === 0) {
      throw new Error("transformed remote editor URI is invalid");
    }
    const pathOffset = encoded.indexOf("/", "file://".length);
    if (pathOffset < 0) {
      throw new Error("transformed remote editor URI path is missing");
    }
    const hostID = createHash("sha256")
      .update(remoteHostname)
      .digest("hex")
      .slice(0, 16);
    return `vscode-remote://${remoteName}+${hostID}` +
      encoded.slice(pathOffset);
  }
  if (uri.scheme !== "vscode-remote") {
    return encoded;
  }
  if (!/^[A-Za-z0-9._~+-]+$/u.test(uri.authority)) {
    throw new Error("remote editor URI authority is invalid");
  }
  const prefix = `${uri.scheme}://`;
  if (!encoded.startsWith(prefix)) {
    throw new Error("remote editor URI is invalid");
  }
  const pathOffset = encoded.indexOf("/", prefix.length);
  if (pathOffset < 0) {
    throw new Error("remote editor URI path is missing");
  }
  return `${prefix}${uri.authority}${encoded.slice(pathOffset)}`;
}
