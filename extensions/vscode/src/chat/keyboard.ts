export type ChatKeyboardAction =
  | "close-sessions"
  | "new-chat"
  | "send"
  | "stop"
  | "none";

export interface ChatKeyboardState {
  readonly key: string;
  readonly ctrlKey: boolean;
  readonly metaKey: boolean;
  readonly shiftKey: boolean;
  readonly isComposing: boolean;
  readonly sessionsOpen: boolean;
  readonly turnActive: boolean;
}

export function routeChatKeyboard(
  state: ChatKeyboardState,
): ChatKeyboardAction {
  if (state.isComposing) return "none";
  if (state.key === "Escape" && state.sessionsOpen) return "close-sessions";
  if (state.key.toLowerCase() === "n" && (state.ctrlKey || state.metaKey)) {
    return "new-chat";
  }
  if (state.key === "Enter" && !state.shiftKey && !state.turnActive) {
    return "send";
  }
  if (state.key === "Escape" && state.turnActive) return "stop";
  return "none";
}
