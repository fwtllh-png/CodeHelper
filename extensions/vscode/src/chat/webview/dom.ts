import type { MarkdownNode } from "../markdown.js";

const markdownTags = new Set([
  "a", "blockquote", "br", "code", "del", "em",
  "h1", "h2", "h3", "h4", "h5", "h6", "hr",
  "li", "ol", "p", "pre", "span", "strong",
  "table", "tbody", "td", "th", "thead", "tr", "ul",
]);
let diagramSequence = 0;
let mermaidLoader: Promise<void> | undefined;

declare global {
  interface Window {
    CodeHelperMermaidRender?: (
      id: string,
      source: string,
    ) => Promise<string>;
  }
}

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
  if (value.tag === "pre" && value.children.length === 1) {
    const code = value.children[0];
    if (code?.kind === "element" && code.tag === "code" &&
      code.language === "mermaid" && code.children.length === 1 &&
      code.children[0]?.kind === "text") {
      return mermaidDiagram(code.children[0].text);
    }
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

function mermaidDiagram(source: string): HTMLElement {
  const figure = document.createElement("figure");
  figure.className = "mermaid-diagram";
  figure.setAttribute("aria-label", "Architecture diagram");
  const status = document.createElement("div");
  status.className = "meta";
  status.textContent = "正在绘制架构图…";
  figure.append(status);
  void renderMermaid(figure, source);
  return figure;
}

async function renderMermaid(figure: HTMLElement, source: string): Promise<void> {
  try {
    await loadMermaid();
    const id = `codehelper-mermaid-${String(++diagramSequence)}`;
    const render = window.CodeHelperMermaidRender;
    if (render === undefined) throw new Error("Mermaid renderer unavailable");
    const rendered = await render(id, source);
    const document = new DOMParser().parseFromString(
      rendered,
      "image/svg+xml",
    );
    const svg = document.documentElement;
    if (svg.nodeName.toLowerCase() !== "svg" ||
      document.querySelector("parsererror") !== null) {
      throw new Error("Mermaid returned invalid SVG");
    }
    for (const unsafe of svg.querySelectorAll(
      "script, foreignObject, iframe, object, embed, a",
    )) {
      unsafe.remove();
    }
    for (const element of [svg, ...svg.querySelectorAll("*")]) {
      for (const attribute of [...element.attributes]) {
        if (/^on/iu.test(attribute.name) ||
          /^(?:href|xlink:href)$/iu.test(attribute.name)) {
          element.removeAttribute(attribute.name);
        }
      }
    }
    const nonce = documentNonce();
    if (nonce !== undefined) {
      for (const style of svg.querySelectorAll("style")) {
        style.setAttribute("nonce", nonce);
      }
    }
    svg.setAttribute("role", "img");
    svg.setAttribute("aria-label", "Architecture diagram");
    figure.replaceChildren(globalThis.document.importNode(svg, true));
  } catch {
    const fallback = globalThis.document.createElement("pre");
    const code = globalThis.document.createElement("code");
    code.className = "language-mermaid";
    code.textContent = source;
    fallback.append(code);
    figure.classList.add("mermaid-fallback");
    figure.replaceChildren(fallback);
  }
}

function loadMermaid(): Promise<void> {
  if (window.CodeHelperMermaidRender !== undefined) return Promise.resolve();
  mermaidLoader ??= new Promise<void>((resolve, reject) => {
    const source = globalThis.document.querySelector<HTMLMetaElement>(
      'meta[name="codehelper-mermaid-renderer"]',
    )?.content;
    const nonce = documentNonce();
    if (source === undefined || source === "" || nonce === undefined) {
      reject(new Error("Mermaid renderer metadata is missing"));
      return;
    }
    const script = globalThis.document.createElement("script");
    script.src = source;
    script.nonce = nonce;
    script.addEventListener("load", () => {
      if (window.CodeHelperMermaidRender === undefined) {
        reject(new Error("Mermaid renderer did not register"));
      } else {
        resolve();
      }
    }, { once: true });
    script.addEventListener("error", () => {
      reject(new Error("Mermaid renderer failed to load"));
    }, { once: true });
    globalThis.document.head.append(script);
  });
  return mermaidLoader;
}

function documentNonce(): string | undefined {
  const value = globalThis.document.querySelector<HTMLMetaElement>(
    'meta[name="codehelper-csp-nonce"]',
  )?.content;
  return value === undefined || value === "" ? undefined : value;
}
