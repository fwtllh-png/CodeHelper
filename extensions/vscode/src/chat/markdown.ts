import MarkdownIt from "markdown-it";
import type Token from "markdown-it/lib/token.mjs";

const maxMarkdownNodes = 8192;
const maxLinkLength = 4096;

export type MarkdownTag =
  | "a"
  | "blockquote"
  | "br"
  | "code"
  | "del"
  | "em"
  | "h1"
  | "h2"
  | "h3"
  | "h4"
  | "h5"
  | "h6"
  | "hr"
  | "li"
  | "ol"
  | "p"
  | "pre"
  | "span"
  | "strong"
  | "table"
  | "tbody"
  | "td"
  | "th"
  | "thead"
  | "tr"
  | "ul";

export type MarkdownNode = MarkdownTextNode | MarkdownElementNode;

export interface MarkdownTextNode {
  readonly kind: "text";
  readonly text: string;
}

export interface MarkdownElementNode {
  readonly kind: "element";
  readonly tag: MarkdownTag;
  readonly children: readonly MarkdownNode[];
  readonly href?: string;
  readonly language?: string;
  readonly start?: number;
}

interface MutableElement {
  readonly kind: "element";
  readonly tag: MarkdownTag;
  readonly children: MarkdownNode[];
  href?: string;
  language?: string;
  start?: number;
}

const markdown = new MarkdownIt({
  breaks: false,
  html: false,
  linkify: true,
  typographer: false,
});

export function projectMarkdown(source: string): readonly MarkdownNode[] {
  try {
    const builder = new MarkdownBuilder();
    builder.appendTokens(markdown.parse(source, {}));
    return builder.nodes;
  } catch {
    return [{
      kind: "element",
      tag: "p",
      children: [{ kind: "text", text: source }],
    }];
  }
}

class MarkdownBuilder {
  public readonly nodes: MarkdownNode[] = [];
  readonly #stack: MutableElement[] = [];
  #nodeCount = 0;

  public appendTokens(tokens: readonly Token[]): void {
    for (const token of tokens) {
      this.#appendToken(token);
    }
    if (this.#stack.length !== 0) {
      throw new Error("unbalanced Markdown token stream");
    }
  }

  #appendToken(token: Token): void {
    if (token.type === "inline") {
      for (const child of token.children ?? []) {
        this.#appendToken(child);
      }
      return;
    }
    if (token.type === "text") {
      this.#text(token.content);
      return;
    }
    if (token.type === "softbreak") {
      this.#text("\n");
      return;
    }
    if (token.type === "hardbreak") {
      this.#leaf("br");
      return;
    }
    if (token.type === "code_inline") {
      this.#element("code", token.content);
      return;
    }
    if (token.type === "fence" || token.type === "code_block") {
      const code: MutableElement = {
        kind: "element",
        tag: "code",
        children: [{ kind: "text", text: token.content }],
      };
      const language = token.info.trim().split(/\s+/u)[0];
      if (language !== undefined && /^[\w+.-]{1,64}$/u.test(language)) {
        code.language = language;
      }
      this.#push({
        kind: "element",
        tag: "pre",
        children: [code],
      });
      return;
    }
    if (token.type === "hr") {
      this.#leaf("hr");
      return;
    }
    if (token.type === "image") {
      this.#element("span", `[image: ${token.content || "attachment"}]`);
      return;
    }
    if (token.nesting === 1) {
      const tag = markdownTag(token.tag);
      const node: MutableElement = { kind: "element", tag, children: [] };
      if (tag === "a") {
        const href = safeHref(token.attrGet("href"));
        if (href !== undefined) node.href = href;
      } else if (tag === "ol") {
        const start = Number(token.attrGet("start") ?? "1");
        if (Number.isSafeInteger(start) && start > 1 && start <= 1_000_000) {
          node.start = start;
        }
      }
      this.#push(node);
      this.#stack.push(node);
      return;
    }
    if (token.nesting === -1) {
      const current = this.#stack.pop();
      if (current === undefined || current.tag !== markdownTag(token.tag)) {
        throw new Error("mismatched Markdown token");
      }
    }
  }

  #element(tag: MarkdownTag, text: string): void {
    this.#push({
      kind: "element",
      tag,
      children: [{ kind: "text", text }],
    });
  }

  #leaf(tag: "br" | "hr"): void {
    this.#push({ kind: "element", tag, children: [] });
  }

  #text(text: string): void {
    if (text.length === 0) return;
    const target = this.#target();
    const previous = target.at(-1);
    if (previous?.kind === "text") {
      target[target.length - 1] = {
        kind: "text",
        text: previous.text + text,
      };
      return;
    }
    this.#push({ kind: "text", text });
  }

  #push(node: MarkdownNode): void {
    this.#nodeCount++;
    if (this.#nodeCount > maxMarkdownNodes) {
      throw new Error("Markdown node budget exceeded");
    }
    this.#target().push(node);
  }

  #target(): MarkdownNode[] {
    return this.#stack.at(-1)?.children ?? this.nodes;
  }
}

function markdownTag(value: string): MarkdownTag {
  switch (value) {
    case "a":
    case "blockquote":
    case "code":
    case "del":
    case "em":
    case "h1":
    case "h2":
    case "h3":
    case "h4":
    case "h5":
    case "h6":
    case "li":
    case "ol":
    case "p":
    case "pre":
    case "s":
    case "span":
    case "strong":
    case "table":
    case "tbody":
    case "td":
    case "th":
    case "thead":
    case "tr":
    case "ul":
      return value === "s" ? "del" : value;
    default:
      throw new Error(`unsupported Markdown tag: ${value}`);
  }
}

function safeHref(value: string | null): string | undefined {
  if (value === null || value.length === 0 || value.length > maxLinkLength) {
    return undefined;
  }
  try {
    const url = new URL(value);
    return ["http:", "https:", "mailto:"].includes(url.protocol)
      ? url.toString()
      : undefined;
  } catch {
    return undefined;
  }
}
