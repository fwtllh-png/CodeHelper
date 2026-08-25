export const maxComposerAttachments = 8;
export const maxTextAttachmentBytes = 64 << 10;
export const maxImageAttachmentBytes = 5 << 20;
export const maxComposerAttachmentBytes = 5 << 20;

export const composerAttachmentAccept = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "text/*",
  ".json",
  ".toml",
  ".yaml",
  ".yml",
  ".md",
  ".csv",
  ".go",
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".py",
  ".rs",
  ".java",
  ".kt",
  ".swift",
  ".c",
  ".cc",
  ".cpp",
  ".h",
  ".hpp",
  ".sh",
  ".sql"
].join(",");
