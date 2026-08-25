import {useEffect, useRef, useState} from "react";
import type {SessionSummary} from "../protocol";
import {
  activityDocumentTitle,
  backgroundNoticeForTransition,
  type BackgroundActivityTarget
} from "./backgroundActivity";
import {
  initialBrowserNotificationSettings,
  notificationSettingsChangedEvent,
  showBrowserNotification
} from "./browserNotifications";

export function BackgroundActivityMonitor({
  sessions,
  selectedSessionID,
  onOpen
}: {
  sessions: readonly SessionSummary[];
  selectedSessionID: string;
  onOpen: (target: BackgroundActivityTarget) => void;
}) {
  const [notificationsEnabled, setNotificationsEnabled] = useState(
    () => initialBrowserNotificationSettings().enabled
  );
  const statusesRef = useRef(new Map(
    sessions.map((session) => [session.session_id, session.status])
  ));
  const notificationsRef = useRef(new Set<Notification>());
  const initialTitleRef = useRef(document.title);

  useEffect(() => {
    document.title = activityDocumentTitle(sessions);
  }, [sessions]);

  useEffect(() => {
    const refresh = () => {
      setNotificationsEnabled(initialBrowserNotificationSettings().enabled);
    };
    window.addEventListener(notificationSettingsChangedEvent, refresh);
    window.addEventListener("storage", refresh);
    return () => {
      window.removeEventListener(notificationSettingsChangedEvent, refresh);
      window.removeEventListener("storage", refresh);
    };
  }, []);

  useEffect(() => () => {
    document.title = initialTitleRef.current || "CodeHelper";
    for (const notification of notificationsRef.current) {
      notification.close();
    }
    notificationsRef.current.clear();
  }, []);

  useEffect(() => {
    const previous = statusesRef.current;
    const next = new Map<string, string>();
    for (const session of sessions) {
      next.set(session.session_id, session.status);
      if (!notificationsEnabled) continue;
      const notice = backgroundNoticeForTransition(
        previous.get(session.session_id),
        session,
        selectedSessionID,
        document.visibilityState === "hidden"
      );
      if (!notice) continue;
      const notification = showBrowserNotification(notice, onOpen);
      if (!notification) continue;
      notificationsRef.current.add(notification);
      notification.onclose = () => {
        notificationsRef.current.delete(notification);
      };
    }
    statusesRef.current = next;
  }, [notificationsEnabled, onOpen, selectedSessionID, sessions]);

  return null;
}
