import {describe, expect, it, vi} from "vitest";

import {
  maxImageAttachmentBytes,
  maxTextAttachmentBytes,
  composerAttachmentAccept
} from "./attachmentLimits";
import {prepareComposerAttachment} from "./attachmentPipeline";

describe("composer attachment pipeline", () => {
  it("turns UTF-8 text into digest-bound durable context", async () => {
    const reference = await prepareComposerAttachment(
      fixtureFile("notes.md", "text/markdown", new TextEncoder().encode("hello\n"))
    );

    expect(reference).toEqual({
      kind: "attachment",
      source: "native_picker",
      digest: "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03",
      label: "notes.md",
      media_type: "text/plain",
      content: "hello\n",
      explicit: true
    });
  });

  it("turns supported images into base64 native model context", async () => {
    const png = Uint8Array.from([
      0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x01
    ]);
    const reference = await prepareComposerAttachment(
      fixtureFile("screen.png", "image/png", png)
    );

    expect(reference).toMatchObject({
      kind: "image",
      source: "native_picker",
      label: "screen.png",
      media_type: "image/png",
      content: "iVBORw0KGgoB",
      explicit: true
    });
    expect(reference.digest).toMatch(/^[0-9a-f]{64}$/);
  });

  it("rejects unsupported, oversized, empty, and invalid UTF-8 files early", async () => {
    await expect(prepareComposerAttachment(
      fixtureFile("archive.zip", "application/zip", Uint8Array.of(1))
    )).rejects.toThrow("not a supported text or image attachment");
    await expect(prepareComposerAttachment(
      sizedFile("large.txt", "text/plain", maxTextAttachmentBytes + 1)
    )).rejects.toThrow("64 KiB text limit");
    await expect(prepareComposerAttachment(
      sizedFile("large.png", "image/png", maxImageAttachmentBytes + 1)
    )).rejects.toThrow("5 MiB image limit");
    await expect(prepareComposerAttachment(
      fixtureFile("empty.txt", "text/plain", new Uint8Array())
    )).rejects.toThrow("empty or contains binary data");
    await expect(prepareComposerAttachment(
      fixtureFile("invalid.txt", "text/plain", Uint8Array.of(0xff))
    )).rejects.toThrow("not valid UTF-8 text");
  });

  it("advertises every supported picker family", () => {
    expect(composerAttachmentAccept).toContain("image/png");
    expect(composerAttachmentAccept).toContain("text/*");
    expect(composerAttachmentAccept).toContain(".tsx");
  });
});

function fixtureFile(
  name: string,
  type: string,
  bytes: Uint8Array
): File {
  return {
    name,
    type,
    size: bytes.byteLength,
    arrayBuffer: vi.fn(async () => Uint8Array.from(bytes).buffer)
  } as unknown as File;
}

function sizedFile(name: string, type: string, size: number): File {
  return {
    name,
    type,
    size,
    arrayBuffer: vi.fn(async () => {
      throw new Error("oversized files must be rejected before reading");
    })
  } as unknown as File;
}
