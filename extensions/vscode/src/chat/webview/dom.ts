import type { MarkdownNode } from "../markdown.js";

const markdownTags = new Set([
  "a", "blockquote", "br", "code", "del", "em",
  "h1", "h2", "h3", "h4", "h5", "h6", "hr",
  "li", "ol", "p", "pre", "span", "strong",
  "table", "tbody", "td", "th", "thead", "tr", "ul",
]);

export function element(id: string): HTMLElement {
  const value = document.getElementById(id);
  if (value === null) throw new Error(`missing Webview element ${id}`);
  return value;
}

export function appendText(
  parent: HTMLElement,
  tag: string,
  className: string,
  text: string,
): void {
  const node = document.createElement(tag);
  if (className.length > 0) node.className = className;
  node.textContent = text;
  parent.append(node);
}

export function appendMarkdown(
  parent: HTMLElement,
  nodes: readonly MarkdownNode[],
  className: string,
  openResource: (resourceId: string) => void,
): void {
  const container = document.createElement("div");
  container.className = `markdown ${className}`;
  for (const node of nodes) {
    container.append(markdownNode(node, openResource));
  }
  parent.append(container);
}

function markdownNode(
  value: MarkdownNode,
  openResource: (resourceId: string) => void,
): Node {
  if (value.kind === "text") return document.createTextNode(value.text);
  if (!markdownTags.has(value.tag)) return document.createTextNode("");
  if (value.resourceId !== undefined) {
    const resourceId = value.resourceId;
    const button = document.createElement("button");
    button.type = "button";
    button.className = "resource-link";
    button.dataset["resourceId"] = resourceId;
    button.title = "Open in Editor";
    for (const child of value.children) {
      button.append(markdownNode(child, openResource));
    }
    button.addEventListener("click", () => {
      openResource(resourceId);
    });
    return button;
  }
  const node = document.createElement(value.tag);
  if (value.tag === "a" && value.href !== undefined &&
    /^(?:https?:|mailto:)/u.test(value.href)) {
    node.setAttribute("href", value.href);
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noreferrer noopener");
  }
  if (value.tag === "ol" && value.start !== undefined &&
    Number.isSafeInteger(value.start) && value.start > 1 &&
    value.start <= 1_000_000) {
    node.setAttribute("start", String(value.start));
  }
  if (value.tag === "code" && value.language !== undefined &&
    /^[\w+.-]{1,64}$/u.test(value.language)) {
    node.className = `language-${value.language}`;
  }
  for (const child of value.children) {
    node.append(markdownNode(child, openResource));
  }
  return node;
}
