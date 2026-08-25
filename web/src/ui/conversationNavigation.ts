import type {ConversationNode} from "../projection/conversation";

export type ConversationNavigationKind =
  | "turn"
  | "question"
  | "tool"
  | "file";

export interface ConversationNavigationItem {
  readonly id: string;
  readonly kind: ConversationNavigationKind;
  readonly entryID: string;
  readonly entryIndex: number;
  readonly turnID: string;
  readonly turnNumber: number;
  readonly label: string;
  readonly detail: string;
  readonly searchText: string;
  readonly callID?: string;
  readonly path?: string;
}

export interface QuestionPosition {
  readonly index: number;
  readonly total: number;
  readonly item?: ConversationNavigationItem;
}

const summaryLength = 140;

export function projectConversationNavigation(
  entries: readonly ConversationNode[]
): readonly ConversationNavigationItem[] {
  const result: ConversationNavigationItem[] = [];
  const turnNumbers = new Map<string, number>();
  const firstByTurn = new Map<string, {entry: ConversationNode; index: number}>();
  const firstQuestionByTurn =
    new Map<string, {entry: Extract<ConversationNode, {kind: "user"}>; index: number}>();

  entries.forEach((entry, index) => {
    if (!turnNumbers.has(entry.turnID)) {
      turnNumbers.set(entry.turnID, turnNumbers.size + 1);
      firstByTurn.set(entry.turnID, {entry, index});
    }
    if (entry.kind === "user" && !firstQuestionByTurn.has(entry.turnID)) {
      firstQuestionByTurn.set(entry.turnID, {entry, index});
    }
  });

  for (const [turnID, first] of firstByTurn) {
    const turnNumber = turnNumbers.get(turnID) ?? 0;
    const prompt = firstQuestionByTurn.get(turnID);
    const detail = nodeSummary(prompt?.entry ?? first.entry);
    result.push(navigationItem({
      id: `turn:${turnID}`,
      kind: "turn",
      entryID: prompt?.entry.id ?? first.entry.id,
      entryIndex: prompt?.index ?? first.index,
      turnID,
      turnNumber,
      label: `Turn ${turnNumber}`,
      detail
    }));
  }

  entries.forEach((entry, entryIndex) => {
    const turnNumber = turnNumbers.get(entry.turnID) ?? 0;
    if (entry.kind === "user") {
      result.push(navigationItem({
        id: `question:${entry.id}`,
        kind: "question",
        entryID: entry.id,
        entryIndex,
        turnID: entry.turnID,
        turnNumber,
        label: compactText(entry.text) || `Question ${turnNumber}`,
        detail: entry.steering ? `Turn ${turnNumber} - steering` : `Turn ${turnNumber}`
      }));
      return;
    }
    if (entry.kind === "tool") {
      result.push(navigationItem({
        id: `tool:${entry.id}`,
        kind: "tool",
        entryID: entry.id,
        entryIndex,
        turnID: entry.turnID,
        turnNumber,
        label: entry.title || entry.tool || "Tool",
        detail: compactText(entry.summary || entry.output || entry.errorSummary) ||
          `Turn ${turnNumber}`,
        callID: entry.callID
      }));
      for (const path of toolPaths(entry)) {
        result.push(navigationItem({
          id: `file:${entry.id}:${path}`,
          kind: "file",
          entryID: entry.id,
          entryIndex,
          turnID: entry.turnID,
          turnNumber,
          label: path,
          detail: `${entry.title || entry.tool || "Tool"} - Turn ${turnNumber}`,
          callID: entry.callID,
          path
        }));
      }
      return;
    }
    if (entry.kind === "deliverables") {
      for (const file of entry.files) {
        result.push(navigationItem({
          id: `file:${entry.id}:${file.path}`,
          kind: "file",
          entryID: entry.id,
          entryIndex,
          turnID: entry.turnID,
          turnNumber,
          label: file.path,
          detail: `${file.kind} - Turn ${turnNumber}`,
          callID: file.callID,
          path: file.path
        }));
      }
    }
  });

  return result;
}

export function searchConversationNavigation(
  items: readonly ConversationNavigationItem[],
  query: string,
  kind: ConversationNavigationKind | "all" = "all",
  limit = 100
): readonly ConversationNavigationItem[] {
  const terms = normalize(query).split(" ").filter(Boolean);
  return items
    .filter((item) => kind === "all" || item.kind === kind)
    .filter((item) => terms.every((term) => item.searchText.includes(term)))
    .map((item, index) => ({
      item,
      index,
      score: navigationScore(item, terms)
    }))
    .sort((left, right) =>
      right.score - left.score ||
      left.item.entryIndex - right.item.entryIndex ||
      left.index - right.index
    )
    .slice(0, limit)
    .map(({item}) => item);
}

export function questionPosition(
  items: readonly ConversationNavigationItem[],
  entryID?: string,
  entryIndex?: number
): QuestionPosition {
  const questions = items.filter((item) => item.kind === "question");
  if (questions.length === 0) return {index: -1, total: 0};
  if (!entryID) {
    return {
      index: questions.length - 1,
      total: questions.length,
      item: questions.at(-1)
    };
  }
  const anchor = items.find((item) => item.entryID === entryID);
  const exact = questions.findIndex((item) => item.entryID === entryID);
  const resolvedEntryIndex = entryIndex ?? anchor?.entryIndex;
  const index = exact >= 0
    ? exact
    : Math.max(
      0,
      lastQuestionAtOrBefore(
        questions,
        resolvedEntryIndex ?? Number.MAX_SAFE_INTEGER
      )
    );
  return {index, total: questions.length, item: questions[index]};
}

export function adjacentQuestion(
  items: readonly ConversationNavigationItem[],
  entryID: string | undefined,
  direction: -1 | 1,
  entryIndex?: number
): ConversationNavigationItem | undefined {
  const position = questionPosition(items, entryID, entryIndex);
  if (position.index < 0) return undefined;
  return items
    .filter((item) => item.kind === "question")
    [position.index + direction];
}

export function transcriptPageForEntry(
  entries: readonly ConversationNode[],
  entryID: string,
  pageSize: number,
  pageStep: number
): number | undefined {
  const index = entries.findIndex((entry) => entry.id === entryID);
  if (index < 0) return undefined;
  return Math.ceil(Math.max(0, entries.length - pageSize - index) / pageStep);
}

function navigationItem(
  item: Omit<ConversationNavigationItem, "searchText">
): ConversationNavigationItem {
  return {
    ...item,
    searchText: normalize([
      item.kind,
      item.label,
      item.detail,
      item.turnID,
      item.callID ?? "",
      item.path ?? ""
    ].join(" "))
  };
}

function navigationScore(
  item: ConversationNavigationItem,
  terms: readonly string[]
): number {
  if (terms.length === 0) return 0;
  const label = normalize(item.label);
  const detail = normalize(item.detail);
  return terms.reduce((score, term) => {
    if (label.startsWith(term)) return score + 4;
    if (label.includes(term)) return score + 3;
    if (detail.includes(term)) return score + 2;
    return score + 1;
  }, 0);
}

function lastQuestionAtOrBefore(
  questions: readonly ConversationNavigationItem[],
  entryIndex: number
): number {
  for (let index = questions.length - 1; index >= 0; index -= 1) {
    if (questions[index]!.entryIndex <= entryIndex) return index;
  }
  return -1;
}

function toolPaths(
  entry: Extract<ConversationNode, {kind: "tool"}>
): readonly string[] {
  const paths = new Set<string>();
  for (const file of entry.editPlan?.files ?? []) addPath(paths, file.path);
  for (const change of entry.changes) {
    addPath(paths, stringValue(change.path));
  }
  if (isObject(entry.arguments)) {
    for (const key of ["path", "file", "file_path"]) {
      addPath(paths, stringValue(entry.arguments[key]));
    }
  }
  return [...paths];
}

function addPath(paths: Set<string>, value: string): void {
  const path = value.trim();
  if (path && path.length <= 512) paths.add(path);
}

function nodeSummary(node: ConversationNode): string {
  if (node.kind === "user" || node.kind === "assistant" ||
      node.kind === "reasoning") {
    return compactText(node.text);
  }
  if (node.kind === "tool") {
    return compactText(node.summary || node.title || node.tool);
  }
  if (node.kind === "status") return compactText(node.text || node.title);
  if (node.kind === "context") return compactText(node.summary || node.title);
  if (node.kind === "deliverables") {
    return compactText(node.files.map((file) => file.path).join(", "));
  }
  return "";
}

function compactText(value: string): string {
  const compact = value.replace(/\s+/g, " ").trim();
  return compact.length > summaryLength
    ? `${compact.slice(0, summaryLength - 1)}...`
    : compact;
}

function normalize(value: string): string {
  return value.normalize("NFKC").toLocaleLowerCase().replace(/\s+/g, " ").trim();
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}
