import type { ChatSessionSummary } from "../runtime/controller.js";
import type { SupervisorState } from "../runtime/supervisor.js";
import {
  deriveChatPresentation,
  type ChatPresentation,
} from "./presentation.js";
import type { ChatSnapshot } from "./projector.js";
import type { ResourceView } from "./resources.js";
import type { ComposerView } from "./composer.js";

export const chatViewProtocolVersion = 1;
export const chatHostMessageTypes = ["snapshot", "error"] as const;

export interface ChatRootView {
  readonly id: string;
  readonly label: string;
}

export interface ChatSessionView extends ChatSessionSummary {
  readonly active: boolean;
}

export interface ChatRuntimeView {
  readonly state: SupervisorState;
  readonly error?: string;
  readonly trusted: boolean;
  readonly selectedRootId: string;
  readonly selectedRootLabel: string;
  readonly selectedSessionId?: string;
  readonly sessions: readonly ChatSessionView[];
  readonly sessionSearch?: {
    readonly query: string;
    readonly sessionIds: readonly string[];
  };
  readonly mergePlanId?: string;
  readonly roots: readonly ChatRootView[];
}

export interface ChatSnapshotMessage {
  readonly type: "snapshot";
  readonly version: typeof chatViewProtocolVersion;
  readonly snapshot: ChatSnapshot;
  readonly resources: readonly ResourceView[];
  readonly runtime: ChatRuntimeView;
  readonly presentation: ChatPresentation;
  readonly composer?: ComposerView;
}

export interface ChatErrorMessage {
  readonly type: "error";
  readonly version: typeof chatViewProtocolVersion;
  readonly message: string;
}

export type ChatHostMessage = ChatSnapshotMessage | ChatErrorMessage;

export interface ChatSnapshotMessageOptions {
  readonly snapshot: ChatSnapshot;
  readonly resources?: readonly ResourceView[];
  readonly state: SupervisorState;
  readonly error?: string;
  readonly trusted: boolean;
  readonly selectedRootId: string;
  readonly selectedRootLabel: string;
  readonly sessions: readonly ChatSessionView[];
  readonly sessionSearch?: {
    readonly query: string;
    readonly sessionIds: readonly string[];
  };
  readonly mergePlanId?: string;
  readonly roots: readonly ChatRootView[];
  readonly composer?: ComposerView;
}

export function createChatSnapshotMessage(
  options: ChatSnapshotMessageOptions,
): ChatSnapshotMessage {
  const selected = options.sessions.find((session) => session.selected);
  return {
    type: "snapshot",
    version: chatViewProtocolVersion,
    snapshot: options.snapshot,
    resources: options.resources?.map((resource) => ({ ...resource })) ?? [],
    presentation: deriveChatPresentation(
      options.state,
      options.snapshot,
      options.trusted,
    ),
    ...(options.composer === undefined ? {} : { composer: options.composer }),
    runtime: {
      state: options.state,
      ...(options.error === undefined ? {} : { error: options.error }),
      trusted: options.trusted,
      selectedRootId: options.selectedRootId,
      selectedRootLabel: options.selectedRootLabel,
      ...(selected === undefined
        ? {}
        : { selectedSessionId: selected.sessionId }),
      sessions: options.sessions.map((session) => ({ ...session })),
      ...(options.sessionSearch === undefined
        ? {}
        : {
            sessionSearch: {
              query: options.sessionSearch.query,
              sessionIds: [...options.sessionSearch.sessionIds],
            },
          }),
      ...(options.mergePlanId === undefined
        ? {}
        : { mergePlanId: options.mergePlanId }),
      roots: options.roots.map((root) => ({ ...root })),
    },
  };
}

export function createChatErrorMessage(message: string): ChatErrorMessage {
  return {
    type: "error",
    version: chatViewProtocolVersion,
    message,
  };
}
