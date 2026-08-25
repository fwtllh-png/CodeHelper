import {fireEvent, render, screen, waitFor} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import type {ConversationNode} from "../projection/conversation";
import type {RuntimeClient} from "../runtime/client";
import {ProducedFiles} from "./ProducedFiles";

Object.defineProperty(URL, "createObjectURL", {
  configurable: true,
  value: vi.fn(() => "blob:produced-file")
});
Object.defineProperty(URL, "revokeObjectURL", {
  configurable: true,
  value: vi.fn()
});
Object.defineProperty(HTMLAnchorElement.prototype, "click", {
  configurable: true,
  value: vi.fn()
});

describe("ProducedFiles", () => {
  it("opens, downloads, diffs, and locates produced workspace files", async () => {
    const client = {
      openWorkspacePath: vi.fn(async () => ({opened: true, path: "main.go"})),
      readWorkspaceResource: vi.fn(async () => ({
        path: "main.go",
        content_handle: "handle"
      })),
      readWorkspaceImage: vi.fn(),
      downloadWorkspaceContent: vi.fn(async () => new Blob(["new\n"])),
      workspaceDiff: vi.fn()
    } as unknown as RuntimeClient;
    const onInspect = vi.fn();
    const entry: Extract<ConversationNode, {kind: "deliverables"}> = {
      id: "deliverables-turn",
      kind: "deliverables",
      turnID: "turn",
      sequence: 5,
      verification: "passed",
      files: [{
        path: "main.go",
        tool: "file_edit",
        kind: "modified",
        added: 1,
        removed: 1,
        summary: "Updated parser",
        callID: "call-edit",
        stale: false,
        diff: {
          path: "main.go",
          kind: "modified",
          before: "old\n",
          after: "new\n",
          beforeExists: true,
          afterExists: true
        }
      }]
    };
    render(
      <ProducedFiles
        entry={entry}
        client={client}
        canOpenPath
        onInspect={onInspect}
        onError={vi.fn()}
      />
    );

    expect(screen.getByText("passed")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Open main.go"}));
    fireEvent.click(screen.getByRole("button", {name: "Download main.go"}));
    fireEvent.click(screen.getByRole("button", {name: "View diff for main.go"}));
    fireEvent.click(screen.getByRole("button", {name: "Inspect tool for main.go"}));

    await waitFor(() => {
      expect(client.openWorkspacePath).toHaveBeenCalledWith("main.go");
      expect(client.downloadWorkspaceContent).toHaveBeenCalledWith("handle");
    });
    expect(screen.getByText("old")).toBeTruthy();
    expect(screen.getByText("new")).toBeTruthy();
    expect(onInspect).toHaveBeenCalledWith("call-edit");
  });
});
