export interface EditorURI {
  readonly scheme: string;
  readonly authority: string;
  toString(): string;
}

export function canonicalEditorURI(
  uri: EditorURI,
): string {
  const encoded = uri.toString();
  if (uri.scheme !== "file" || uri.authority !== "" ||
    !encoded.startsWith("file:///")) {
    throw new Error("CodeHelper VS Code supports only local file URIs");
  }
  return encoded;
}
