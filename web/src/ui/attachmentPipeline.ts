import type {EditorContextReference} from "../protocol";
import {
  maxImageAttachmentBytes,
  maxTextAttachmentBytes
} from "./attachmentLimits";

export type ComposerAttachmentSource = "picker" | "drop" | "paste";

export interface ComposerAttachment {
  id: string;
  name: string;
  mediaType: string;
  bytes: number;
  source: ComposerAttachmentSource;
  status: "processing" | "ready" | "error";
  error?: string;
  digest?: string;
}

const imageTypes = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp"
]);

const textTypes = new Set([
  "",
  "application/json",
  "application/javascript",
  "application/typescript",
  "application/xml",
  "application/yaml",
  "text/csv",
  "text/html",
  "text/javascript",
  "text/markdown",
  "text/plain",
  "text/xml",
  "text/yaml"
]);

const textExtensions = new Set([
  "c", "cc", "conf", "cpp", "css", "csv", "go", "h", "hpp", "html",
  "ini", "java", "js", "json", "jsx", "kt", "log", "md", "mjs", "py",
  "rs", "sh", "sql", "swift", "toml", "ts", "tsx", "txt", "xml", "yaml", "yml"
]);

export async function prepareComposerAttachment(
  file: File
): Promise<EditorContextReference> {
  const mediaType = file.type.toLowerCase();
  const name = file.name || pastedImageName(mediaType);
  if (imageTypes.has(mediaType)) {
    if (file.size > maxImageAttachmentBytes) {
      throw new Error(`${name} exceeds the 5 MiB image limit`);
    }
    const data = new Uint8Array(await file.arrayBuffer());
    if (data.byteLength === 0) {
      throw new Error(`${name} is empty`);
    }
    return {
      kind: "image",
      source: "native_picker",
      digest: await sha256(data),
      label: name,
      media_type: mediaType,
      content: encodeBase64(data),
      explicit: true
    };
  }
  if (!isTextFile(name, mediaType)) {
    throw new Error(`${name} is not a supported text or image attachment`);
  }
  if (file.size > maxTextAttachmentBytes) {
    throw new Error(`${name} exceeds the 64 KiB text limit`);
  }
  const data = new Uint8Array(await file.arrayBuffer());
  let content: string;
  try {
    content = new TextDecoder("utf-8", {fatal: true}).decode(data);
  } catch {
    throw new Error(`${name} is not valid UTF-8 text`);
  }
  if (!content || content.includes("\0")) {
    throw new Error(`${name} is empty or contains binary data`);
  }
  return {
    kind: "attachment",
    source: "native_picker",
    digest: await sha256(data),
    label: name,
    media_type: "text/plain",
    content,
    explicit: true
  };
}

function isTextFile(name: string, mediaType: string): boolean {
  if (mediaType.startsWith("text/") || textTypes.has(mediaType)) return true;
  const extension = name.toLowerCase().split(".").at(-1) ?? "";
  return textExtensions.has(extension);
}

async function sha256(data: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    Uint8Array.from(data).buffer
  );
  return [...new Uint8Array(digest)]
    .map((value) => value.toString(16).padStart(2, "0"))
    .join("");
}

function encodeBase64(data: Uint8Array): string {
  let binary = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < data.length; offset += chunkSize) {
    binary += String.fromCharCode(...data.subarray(offset, offset + chunkSize));
  }
  return btoa(binary);
}

function pastedImageName(mediaType: string): string {
  const extension = mediaType.split("/")[1]?.replace("jpeg", "jpg") || "png";
  return `Pasted image.${extension}`;
}
