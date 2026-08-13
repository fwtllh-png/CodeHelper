import type {
  DiagnosticsResultData, TurnReceiptData, TurnStartedData,
} from "../../protocol/generated.js";
import type { EditPlanCard } from "../../edits/model.js";
import type { MarkdownNode } from "../markdown.js";
import type { ResourceRange } from "../resources.js";
export type { EditPlanCard, EditPlanFileCard } from "../../edits/model.js";
export const maxCardText = 64 << 10;
export const maxChatTurns = 200;
export const expandedContextMarker = "\n\nExplicit editor context follows as JSON.";
export type TurnStatus = "running" | "awaiting_approval" | "awaiting_input" |
  "completed" | "failed" | "canceled";
export interface ToolCard {
  readonly callId: string; readonly tool: string; readonly status: "running" | "completed" | "failed";
  readonly changes: readonly FileChangeCard[]; readonly arguments?: string; readonly output: string;
}
export interface FileChangeCard {
  readonly path: string; readonly resourceId?: string;
  readonly kind: "created" | "modified" | "deleted"; readonly added: number; readonly removed: number;
}
export interface ApprovalCard {
  readonly requestId: string; readonly turnId: string; readonly itemId: string;
  readonly tool: string; readonly arguments: string; readonly resources: readonly string[];
  readonly allowedScopes: readonly string[]; readonly expiresAt: string;
  readonly reason?: string; readonly resolved?: string; readonly editPlan?: EditPlanCard;
  readonly source?: {
    readonly kind: "agent";
    readonly agentId: string;
    readonly agentPath: string;
    readonly parentPath: string;
    readonly role: string;
  };
}
export interface InputCard {
  readonly requestId: string; readonly turnId: string; readonly itemId: string;
  readonly prompt: string; readonly options: readonly string[]; readonly expiresAt: string; readonly resolved?: string;
}
export interface PlanCard {
  readonly id?: string; readonly body: string; readonly bodyMarkdown: readonly MarkdownNode[];
  readonly status: "drafting" | "ready"; readonly canImplement: boolean; readonly canAutopilot: boolean;
}
export interface WorkspaceChangeCard { readonly changedCount: number; readonly workspace?: string }
interface TimelineBase { readonly id: string; readonly sequence: number }
export type TurnTimelineItem =
  | TimelineBase & {
      readonly kind: "output"; readonly text: string;
      readonly markdown: readonly MarkdownNode[]; readonly final: boolean;
    }
  | TimelineBase & {
      readonly kind: "reasoning"; readonly text: string;
      readonly markdown: readonly MarkdownNode[]; readonly active: boolean;
    }
  | TimelineBase & (
      | { readonly kind: "tool"; readonly callId: string }
      | { readonly kind: "approval" | "input"; readonly requestId: string }
      | { readonly kind: "diagnostics"; readonly messages: readonly string[] }
      | { readonly kind: "verification" | "notice"; readonly text: string }
    );
export type EditorContextReceipt = NonNullable<TurnStartedData["editor_context"]>[number];
export interface ContextReceiptCard {
  readonly kind: EditorContextReceipt["kind"]; readonly source?: EditorContextReceipt["source"];
  readonly path: string; readonly label?: string; readonly digest: string;
  readonly range?: string; readonly navigationRange?: ResourceRange;
  readonly symbol?: string; readonly symbolName?: string; readonly resourceId?: string;
  readonly diagnosticCount: number; readonly omittedDiagnostics: number;
  readonly originalBytes: number; readonly retainedBytes: number;
  readonly truncated: boolean;
}
export type ContextSelection = NonNullable<TurnReceiptData["context_selections"]>[number];
export interface ContextSelectionCard {
  readonly path: string; readonly kind: string;
  readonly reasons: readonly string[]; readonly evidence: readonly string[];
  readonly score: number; readonly critical: boolean; readonly included: boolean;
  readonly truncated: boolean; readonly truncationReason?: string; readonly resourceId?: string;
}
export interface ChatTurn {
  readonly id: string; readonly user: string; readonly status: TurnStatus;
  readonly output: string; readonly reasoning: string;
  readonly outputMarkdown: readonly MarkdownNode[]; readonly reasoningMarkdown: readonly MarkdownNode[];
  readonly reasoningActive: boolean; readonly timeline: readonly TurnTimelineItem[];
  readonly tools: readonly ToolCard[]; readonly approvals: readonly ApprovalCard[];
  readonly inputs: readonly InputCard[];
  readonly plan?: PlanCard; readonly contextReceipts: readonly ContextReceiptCard[];
  readonly contextSelections: readonly ContextSelectionCard[]; readonly diagnostics: readonly string[];
  readonly verification?: string; readonly receipt?: string; readonly error?: string;
  readonly workspaceChange?: WorkspaceChangeCard;
  readonly unknownEvents: readonly string[];
}
export interface ChatSnapshot { readonly turns: readonly ChatTurn[]; readonly activeTurnId?: string }
type Mutable<T> = { -readonly [Key in keyof T]: T[Key] };
export type MutableTool = Omit<Mutable<ToolCard>, "changes"> &
  { changes: FileChangeCard[] };
export type MutableApproval =
  Omit<Mutable<ApprovalCard>, "resources" | "allowedScopes"> &
  { resources: string[]; allowedScopes: string[] };
export type MutableInput = Omit<Mutable<InputCard>, "options"> & { options: string[] };
export type DiagnosticReceipt = DiagnosticsResultData["receipts"][number];
export type MutableTimelineItem =
  | Mutable<Omit<Extract<TurnTimelineItem, { kind: "output" }>, "markdown" | "final">>
  | Mutable<Omit<Extract<TurnTimelineItem, { kind: "reasoning" }>, "markdown" | "active">>
  | Mutable<Extract<TurnTimelineItem, { kind: "tool" | "approval" | "input" }>>
  | Omit<Mutable<Extract<TurnTimelineItem, { kind: "diagnostics" }>>, "messages"> & {
      messages: string[];
    }
  | Mutable<Extract<TurnTimelineItem, { kind: "verification" | "notice" }>>;
export interface MutableTurn {
  id: string; user: string; status: TurnStatus;
  output: string; reasoning: string; reasoningActive: boolean;
  timeline: MutableTimelineItem[]; tools: Map<string, MutableTool>;
  approvals: Map<string, MutableApproval>; inputs: Map<string, MutableInput>;
  plan?: Omit<PlanCard, "bodyMarkdown">;
  contextReceipts: ContextReceiptCard[]; contextSelections: ContextSelectionCard[];
  diagnostics: Map<string, DiagnosticReceipt>; diagnosticNotices: string[];
  workspace?: string; workspaceIsolation?: string;
  workspaceChange?: WorkspaceChangeCard; lastSequence: number;
  verification?: string; receipt?: string; error?: string; unknownEvents: string[];
}
