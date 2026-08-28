import {afterEach, describe, expect, it, vi} from "vitest";
import type {SessionSummary} from "../protocol";
import {
  activityDocumentTitle,
  backgroundNoticeForTransition,
  sessionStatusPresentation
} from "./backgroundActivity";
import {
  notificationPreferenceKey,
  setBrowserNotificationsEnabled
} from "./browserNotifications";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("background activity", () => {
  it("projects page title and visible status labels from Session summaries", () => {
    expect(activityDocumentTitle([
      session("running", "one"),
      session("awaiting_approval", "two"),
      session("failed", "three")
    ])).toBe("(1) Action required · CodeHelper");
    expect(activityDocumentTitle([
      session("running", "one"),
      session("running", "two")
    ])).toBe("(2) Working · CodeHelper");
    expect(activityDocumentTitle([
      session("interrupted", "paused")
    ])).toBe("(1) Paused · CodeHelper");
    expect(activityDocumentTitle([
      session("blocked", "blocked")
    ])).toBe("(1) Blocked · CodeHelper");
    expect(sessionStatusPresentation("completed")).toEqual({
      label: "Completed",
      tone: "complete"
    });
    expect(sessionStatusPresentation("interrupted")).toEqual({
      label: "Paused",
      tone: "warning"
    });
    expect(sessionStatusPresentation("blocked")).toEqual({
      label: "Blocked",
      tone: "warning"
    });
  });

  it("emits privacy-safe notices only for background state transitions", () => {
    const value = {
      ...session("awaiting_approval", "review"),
      title: "secret prompt contents",
      latest_turn_id: "turn-review"
    };
    const notice = backgroundNoticeForTransition(
      "running",
      value,
      "foreground",
      false
    );

    expect(notice).toMatchObject({
      title: "CodeHelper needs approval",
      body: "A background Session is waiting for approval.",
      target: {
        sessionID: "review",
        turnID: "turn-review",
        status: "awaiting_approval"
      }
    });
    expect(JSON.stringify(notice)).not.toContain(value.title);
    expect(backgroundNoticeForTransition(
      undefined,
      value,
      "foreground",
      false
    )).toBeUndefined();
    expect(backgroundNoticeForTransition(
      "running",
      value,
      value.session_id,
      false
    )).toBeUndefined();
  });

  it("keeps notifications off until browser permission is granted", async () => {
    const preferences = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (key: string) => preferences.get(key) ?? null,
      setItem: (key: string, value: string) => preferences.set(key, value),
      removeItem: (key: string) => preferences.delete(key)
    });
    class TestNotification {
      static permission: NotificationPermission = "default";
      static requestPermission = vi.fn(async () => "granted" as NotificationPermission);
    }
    vi.stubGlobal("Notification", TestNotification);

    expect(preferences.has(notificationPreferenceKey)).toBe(false);
    await expect(setBrowserNotificationsEnabled(true)).resolves.toEqual({
      enabled: true,
      permission: "granted"
    });
    expect(TestNotification.requestPermission).toHaveBeenCalledOnce();
    expect(preferences.get(notificationPreferenceKey)).toBe("true");
  });
});

function session(status: string, id: string): SessionSummary {
  return {
    version: 1,
    revision: 1,
    session_id: id,
    thread_id: `thread-${id}`,
    title: id,
    status,
    pinned: false,
    archived: false,
    isolation: "shared",
    workspace_root: "/workspace",
    workspace_label: "workspace",
    latest_turn_id: `turn-${id}`,
    latest_sequence: 1,
    pending_approvals: status === "awaiting_approval" ? 1 : 0,
    pending_inputs: status === "awaiting_input" ? 1 : 0,
    checkpoint_count: 0,
    changed_files: 0,
    total_tokens: 0,
    cost_microunits: 0,
    cost_known: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z"
  };
}
