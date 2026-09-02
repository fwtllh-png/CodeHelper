import type {
  BackgroundActivityTarget,
  BackgroundNotice,
  BrowserNotificationPermission,
  BrowserNotificationSettings
} from "./backgroundActivity";

export const notificationPreferenceKey = "ch.notifications.enabled";
export const notificationSettingsChangedEvent =
  "qcode:notification-settings-changed";

export function initialBrowserNotificationSettings(): BrowserNotificationSettings {
  const permission = browserNotificationPermission();
  return {
    enabled: readNotificationPreference() && permission === "granted",
    permission
  };
}

export async function setBrowserNotificationsEnabled(
  enabled: boolean
): Promise<BrowserNotificationSettings> {
  if (!enabled) {
    writeNotificationPreference(false);
    const settings = {
      enabled: false,
      permission: browserNotificationPermission()
    };
    notifySettingsChanged();
    return settings;
  }
  if (typeof Notification === "undefined") {
    writeNotificationPreference(false);
    const settings = {enabled: false, permission: "unsupported" as const};
    notifySettingsChanged();
    return settings;
  }
  const permission = Notification.permission === "default"
    ? await Notification.requestPermission()
    : Notification.permission;
  const granted = permission === "granted";
  writeNotificationPreference(granted);
  const settings = {enabled: granted, permission};
  notifySettingsChanged();
  return settings;
}

export function showBrowserNotification(
  notice: BackgroundNotice,
  onClick: (target: BackgroundActivityTarget) => void
): Notification | undefined {
  if (
    typeof Notification === "undefined" ||
    Notification.permission !== "granted"
  ) {
    return undefined;
  }
  try {
    const notification = new Notification(notice.title, {
      body: notice.body,
      tag: notice.tag
    });
    notification.onclick = () => {
      notification.close();
      onClick(notice.target);
    };
    return notification;
  } catch {
    return undefined;
  }
}

function browserNotificationPermission(): BrowserNotificationPermission {
  return typeof Notification === "undefined"
    ? "unsupported"
    : Notification.permission;
}

function readNotificationPreference(): boolean {
  try {
    return window.localStorage?.getItem(notificationPreferenceKey) === "true";
  } catch {
    return false;
  }
}

function writeNotificationPreference(enabled: boolean): void {
  try {
    window.localStorage?.setItem(notificationPreferenceKey, String(enabled));
  } catch {
    // Browser preferences are optional and never become Runtime state.
  }
}

function notifySettingsChanged(): void {
  window.dispatchEvent(new Event(notificationSettingsChangedEvent));
}
