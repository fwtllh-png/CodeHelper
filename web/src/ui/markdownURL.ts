export function safeMarkdownURL(value: string, key: string): string {
  if (key === "src") return safeImageSource(value) ?? "";
  if (value.startsWith("#") || workspacePathFromHref(value)) return value;
  return isExternalURL(value) || value.startsWith("mailto:") ? value : "";
}

export function safeImageSource(
  value: string | undefined
): string | undefined {
  if (!value) return undefined;
  try {
    const url = new URL(value, window.location.origin);
    return url.origin === window.location.origin || url.protocol === "https:"
      ? url.toString()
      : undefined;
  } catch {
    return undefined;
  }
}

export function workspacePathFromHref(
  value: string | undefined
): string | undefined {
  if (!value || value.startsWith("#") || isExternalURL(value) ||
      value.startsWith("mailto:")) {
    return undefined;
  }
  if (/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value) &&
      !value.startsWith("file://")) {
    return undefined;
  }
  const path = value.split(/[?#]/, 1)[0]?.trim();
  if (!path) return undefined;
  try {
    return decodeURIComponent(path.replace(/^file:\/\//, ""));
  } catch {
    return path.replace(/^file:\/\//, "");
  }
}

export function isExternalURL(value: string | undefined): boolean {
  return value?.startsWith("https://") === true ||
    value?.startsWith("http://") === true;
}

export function isCrossOrigin(source: string): boolean {
  try {
    return new URL(source).origin !== window.location.origin;
  } catch {
    return true;
  }
}

export function imageOrigin(source: string): string {
  try {
    const url = new URL(source);
    return url.origin === window.location.origin ? "QCode" : url.host;
  } catch {
    return "Image";
  }
}
