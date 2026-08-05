import type { EditorContextReference, SubmitReceipt } from "../runtime/session.js";
import {
  normalizeInstruction,
  selectionPrompt,
  type SelectionCommandSpec,
} from "./policy.js";

export interface SelectionFlowDependencies {
  isTrusted(): boolean;
  requestInstruction(spec: SelectionCommandSpec): Promise<string | undefined>;
  captureSelection(): Promise<readonly EditorContextReference[]>;
  submit(
    prompt: string,
    context: readonly EditorContextReference[],
  ): Promise<SubmitReceipt>;
  focusChat(): Promise<void>;
}

export class SelectionFlow {
  readonly #dependencies: SelectionFlowDependencies;

  public constructor(dependencies: SelectionFlowDependencies) {
    this.#dependencies = dependencies;
  }

  public async execute(
    spec: SelectionCommandSpec,
    suppliedInstruction?: string,
  ): Promise<SubmitReceipt | undefined> {
    if (spec.requiresTrust && !this.#dependencies.isTrusted()) {
      throw new Error(`${spec.title} is unavailable in an untrusted workspace`);
    }
    let instruction: string | undefined;
    if (spec.requiresInstruction) {
      const value = suppliedInstruction ??
        await this.#dependencies.requestInstruction(spec);
      if (value === undefined) {
        return undefined;
      }
      instruction = normalizeInstruction(value);
    }
    const context = await this.#dependencies.captureSelection();
    await this.#dependencies.focusChat();
    return this.#dependencies.submit(
      selectionPrompt(spec.id, instruction),
      context,
    );
  }
}
