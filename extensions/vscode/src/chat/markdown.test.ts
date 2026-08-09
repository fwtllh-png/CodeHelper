import assert from "node:assert/strict";
import test from "node:test";

import {
  projectMarkdown,
  type MarkdownElementNode,
  type MarkdownNode,
} from "./markdown.js";

void test("projectMarkdown preserves structured prose, lists, tables, and code", () => {
  const nodes = projectMarkdown([
    "# Result",
    "",
    "**bold** and `code`",
    "",
    "- first",
    "- second",
    "",
    "| Name | Value |",
    "| --- | --- |",
    "| a | b |",
    "",
    "```go",
    "fmt.Println(\"ok\")",
    "```",
  ].join("\n"));

  assert.deepEqual(elementTags(nodes), [
    "h1", "p", "strong", "code", "ul", "li", "p", "li", "p",
    "table", "thead", "tr", "th", "th", "tbody", "tr", "td", "td",
    "pre", "code",
  ]);
  const code = elements(nodes).find((node) =>
    node.tag === "code" && node.language === "go");
  assert.equal(code?.children[0]?.kind, "text");
});

void test("projectMarkdown disables raw HTML and unsafe link protocols", () => {
  const nodes = projectMarkdown(
    '<script>alert(1)</script> [bad](command:workbench.action.closeWindow) ' +
    "[good](https://example.com/path)",
  );
  assert.equal(JSON.stringify(nodes).includes("<script>alert(1)</script>"), true);
  const links = elements(nodes).filter((node) => node.tag === "a");
  assert.equal(links.length, 2);
  assert.equal(links[0]?.href, undefined);
  assert.equal(links[1]?.href, "https://example.com/path");
});

void test("projectMarkdown preserves local resources and Mermaid fences", () => {
  const nodes = projectMarkdown([
    "[source](src/chat/view.ts#L10-L20)",
    "",
    "```mermaid",
    "flowchart TD",
    "  A --> B",
    "```",
  ].join("\n"));
  const values = elements(nodes);
  assert.equal(
    values.find((node) => node.tag === "a")?.href,
    "src/chat/view.ts#L10-L20",
  );
  assert.equal(
    values.find((node) => node.tag === "code" && node.language === "mermaid")
      ?.children[0]?.kind,
    "text",
  );
});

function elementTags(nodes: readonly MarkdownNode[]): string[] {
  return elements(nodes).map((node) => node.tag);
}

function elements(nodes: readonly MarkdownNode[]): MarkdownElementNode[] {
  const result: MarkdownElementNode[] = [];
  for (const node of nodes) {
    if (node.kind !== "element") continue;
    result.push(node);
    result.push(...elements(node.children));
  }
  return result;
}
