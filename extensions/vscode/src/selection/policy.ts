export type SelectionCommandID =
  | "codehelper.explainSelection"
  | "codehelper.editSelection"
  | "codehelper.refactorSelection"
  | "codehelper.generateTestsForSelection";

export interface SelectionCommandSpec {
  readonly id: SelectionCommandID;
  readonly title: string;
  readonly requiresInstruction: boolean;
  readonly requiresTrust: boolean;
}

export const selectionCommandSpecs: readonly SelectionCommandSpec[] = [
  {
    id: "codehelper.explainSelection",
    title: "Explain Selection",
    requiresInstruction: false,
    requiresTrust: false,
  },
  {
    id: "codehelper.editSelection",
    title: "Edit Selection",
    requiresInstruction: true,
    requiresTrust: true,
  },
  {
    id: "codehelper.refactorSelection",
    title: "Refactor Selection",
    requiresInstruction: true,
    requiresTrust: true,
  },
  {
    id: "codehelper.generateTestsForSelection",
    title: "Generate Tests for Selection",
    requiresInstruction: false,
    requiresTrust: true,
  },
] as const;

const maxInstructionLength = 4096;

export function selectionPrompt(
  command: SelectionCommandID,
  instruction?: string,
): string {
  switch (command) {
    case "codehelper.explainSelection":
      return "Explain the selected code, including its behavior and important constraints.";
    case "codehelper.editSelection":
      return `Edit the selected code according to this instruction:\n\n` +
        normalizeInstruction(instruction);
    case "codehelper.refactorSelection":
      return `Refactor the selected code according to this instruction:\n\n` +
        normalizeInstruction(instruction);
    case "codehelper.generateTestsForSelection":
      return "Generate focused tests for the selected code using the repository's existing test patterns.";
  }
}

export function normalizeInstruction(value: string | undefined): string {
  const normalized = value?.trim() ?? "";
  if (normalized.length === 0) {
    throw new Error("selection instruction is required");
  }
  if (normalized.length > maxInstructionLength) {
    throw new Error(
      `selection instruction exceeds ${String(maxInstructionLength)} characters`,
    );
  }
  return normalized;
}
