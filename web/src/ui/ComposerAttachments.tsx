import {FileText, Image, LoaderCircle, TriangleAlert, X} from "lucide-react";

import type {ComposerAttachment} from "./attachmentPipeline";
import "./ComposerAttachments.css";

export function ComposerAttachments({
  attachments,
  onRemove
}: {
  attachments: readonly ComposerAttachment[];
  onRemove: (id: string) => void;
}) {
  if (attachments.length === 0) return null;
  return (
    <div
      className="attachmentTray"
      aria-label="Composer attachments"
      aria-live="polite"
    >
      {attachments.map((attachment) => (
        <div
          className="attachmentItem"
          data-status={attachment.status}
          key={attachment.id}
        >
          {attachment.status === "processing"
            ? <LoaderCircle className="spin" size={15} />
            : attachment.status === "error"
              ? <TriangleAlert size={15} />
              : attachment.mediaType.startsWith("image/")
                ? <Image size={15} />
                : <FileText size={15} />}
          <span>
            <strong>{attachment.name}</strong>
            <small>
              {attachment.status === "error"
                ? `Error · ${attachment.source} · ${attachment.error}`
                : attachment.status === "processing"
                  ? `Processing · ${attachment.source}`
                  : `${formatMediaType(attachment.mediaType)} · ${formatBytes(
                      attachment.bytes
                    )} · ${attachment.source}`}
            </small>
          </span>
          <button
            type="button"
            aria-label={`Remove attachment ${attachment.name}`}
            title="Remove attachment"
            onClick={() => onRemove(attachment.id)}
          >
            <X size={13} />
          </button>
        </div>
      ))}
    </div>
  );
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 102.4) / 10} KiB`;
  return `${Math.round(value / (1024 * 102.4)) / 10} MiB`;
}

function formatMediaType(value: string): string {
  if (!value || value === "application/octet-stream") return "File";
  if (value.startsWith("image/")) return value.slice("image/".length).toUpperCase();
  if (value === "text/plain") return "Text";
  return value;
}
