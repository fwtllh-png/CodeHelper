import {cleanup, fireEvent, render, screen, waitFor} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import type {ConversationNavigationItem} from "./conversationNavigation";
import {ConversationNavigator} from "./ConversationNavigator";

afterEach(cleanup);

const items: ConversationNavigationItem[] = [
  item("turn:one", "turn", "user-one", "Turn 1", "Inspect parser", 1),
  item(
    "question:user-one",
    "question",
    "user-one",
    "Inspect parser",
    "Turn 1",
    1
  ),
  {
    ...item(
      "tool:read",
      "tool",
      "tool-read",
      "Read README.md",
      "Read project overview",
      1
    ),
    callID: "call-read"
  },
  {
    ...item(
      "file:read:README.md",
      "file",
      "tool-read",
      "README.md",
      "Read README.md - Turn 1",
      1
    ),
    callID: "call-read",
    path: "README.md"
  }
];

describe("ConversationNavigator", () => {
  it("filters results and selects the active stable identity by keyboard", () => {
    const onSelect = vi.fn();
    render(
      <ConversationNavigator
        items={items}
        currentEntryID="user-one"
        hasEarlier={false}
        onClose={vi.fn()}
        onSelect={onSelect}
        onLoadEarlier={vi.fn(async () => 0)}
      />
    );

    fireEvent.click(screen.getByRole("tab", {name: "Files"}));
    expect(screen.getAllByRole("option")).toHaveLength(1);
    fireEvent.keyDown(
      screen.getByRole("dialog", {name: "Search conversation"}),
      {key: "Enter"}
    );
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({
      id: "file:read:README.md",
      path: "README.md"
    }));
  });

  it("loads earlier history without dismissing search", async () => {
    const onLoadEarlier = vi.fn(async () => 20);
    render(
      <ConversationNavigator
        items={items}
        hasEarlier
        onClose={vi.fn()}
        onSelect={vi.fn()}
        onLoadEarlier={onLoadEarlier}
      />
    );

    fireEvent.click(screen.getByRole("button", {name: "Load earlier history"}));
    await waitFor(() => expect(onLoadEarlier).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("dialog", {name: "Search conversation"})).toBeTruthy();
    expect(screen.getByRole("combobox", {name: "Search conversation"}))
      .toBe(document.activeElement);
  });
});

function item(
  id: string,
  kind: ConversationNavigationItem["kind"],
  entryID: string,
  label: string,
  detail: string,
  turnNumber: number
): ConversationNavigationItem {
  return {
    id,
    kind,
    entryID,
    entryIndex: 0,
    turnID: "turn-one",
    turnNumber,
    label,
    detail,
    searchText: `${kind} ${label} ${detail}`.toLocaleLowerCase()
  };
}
