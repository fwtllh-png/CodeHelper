import {cleanup, fireEvent, render, screen, waitFor} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";
import {MarkdownMessage} from "./MarkdownMessage";

const clipboardWrite = vi.fn(async () => {});

Object.defineProperty(navigator, "clipboard", {
  configurable: true,
  value: {writeText: clipboardWrite}
});

afterEach(() => {
  cleanup();
  clipboardWrite.mockClear();
});

describe("MarkdownMessage", () => {
  it("renders math, CJK emphasis, nested content, wide tables, and code", async () => {
    const openFile = vi.fn();
    const columns = Array.from({length: 8}, (_, index) => `Column ${index + 1}`);
    const values = Array.from({length: 8}, (_, index) => `value-${index + 1}`);
    const text = [
      "**注意：**内容保持连续。",
      "",
      "Inline math $E = mc^2$.",
      "",
      "$$",
      "\\sum_{i=1}^{n} i = \\frac{n(n+1)}{2}",
      "$$",
      "",
      "> Quoted guidance",
      "> 1. First",
      ">    - Nested",
      "",
      "[Open source](src/main.ts#L12)",
      "",
      "[Unsafe](javascript:alert(1))",
      "",
      `| ${columns.join(" | ")} |`,
      `| ${columns.map(() => "---").join(" | ")} |`,
      `| ${values.join(" | ")} |`,
      "",
      "```typescript",
      `const path = "${"workspace/".repeat(30)}file.ts";`,
      "```"
    ].join("\n");

    const {container} = render(
      <MarkdownMessage
        text={text}
        settled
        canOpenPath
        onOpenFile={openFile}
      />
    );

    expect(screen.getByText("注意：").tagName).toBe("STRONG");
    await waitFor(() => {
      expect(container.querySelectorAll(".katex")).toHaveLength(2);
      expect(container.querySelector('math[display="block"]')).toBeTruthy();
    });
    expect(screen.getByRole("region", {name: "Response table"})).toBeTruthy();
    expect(screen.getByRole("region", {name: "Response table"})
      .querySelectorAll("th")).toHaveLength(8);
    expect(screen.getByText("Quoted guidance").closest("blockquote")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", {name: "Open file src/main.ts"}));
    expect(openFile).toHaveBeenCalledWith("src/main.ts");
    expect(screen.queryByRole("link", {name: "Unsafe"})).toBeNull();
    expect(screen.getByText("Unsafe")).toBeTruthy();

    const code = screen.getByRole("button", {name: "Copy code"});
    expect(container.querySelector(".markdownCodeBlock pre")?.textContent)
      .toContain("workspace/workspace/");
    fireEvent.click(code);
    await waitFor(() => {
      expect(clipboardWrite).toHaveBeenCalledWith(expect.stringContaining(
        "const path"
      ));
    });
  });

  it("loads remote images only after consent and exposes failure recovery", () => {
    const sameOrigin = `${window.location.origin}/icon-192.png`;
    render(
      <MarkdownMessage
        text={[
          `![Local diagram](${sameOrigin})`,
          "",
          "![Remote diagram](https://images.example.test/diagram.png)"
        ].join("\n")}
        settled
        canOpenPath={false}
        onOpenFile={() => {}}
      />
    );

    expect(screen.getByRole("img", {name: "Local diagram"})).toBeTruthy();
    expect(screen.queryByRole("img", {name: "Remote diagram"})).toBeNull();
    fireEvent.click(screen.getByRole("button", {name: "Load image Remote diagram"}));
    const remote = screen.getByRole("img", {name: "Remote diagram"});
    expect(remote.getAttribute("referrerpolicy")).toBe("no-referrer");
    expect(remote.getAttribute("loading")).toBe("lazy");
    fireEvent.error(remote);
    expect(screen.getByRole("alert").textContent).toBe("Image unavailable");
    expect(screen.getByRole("button", {
      name: "Retry image Remote diagram"
    })).toBeTruthy();
    expect(screen.getByRole("link", {
      name: "Download image Remote diagram"
    })).toBeTruthy();
  });
});
