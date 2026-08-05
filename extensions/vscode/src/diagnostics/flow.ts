import type { EditorContextReference, SubmitReceipt } from "../runtime/session.js";
import {
  diagnosticPrompt,
  type DiagnosticAction,
  type DiagnosticSnapshot,
} from "./policy.js";

export interface DiagnosticFlowDependencies {
  isTrusted(): boolean;
  captureDiagnostic(
    snapshot: DiagnosticSnapshot,
  ): Promise<readonly EditorContextReference[]>;
  focusChat(): Promise<void>;
  submit(
    prompt: string,
    context: readonly EditorContextReference[],
  ): Promise<SubmitReceipt>;
}

export class DiagnosticFlow {
  readonly #dependencies: DiagnosticFlowDependencies;

  public constructor(dependencies: DiagnosticFlowDependencies) {
    this.#dependencies = dependencies;
  }

  public async execute(
    action: DiagnosticAction,
    snapshot: DiagnosticSnapshot,
  ): Promise<SubmitReceipt> {
    if (action === "fix" && !this.#dependencies.isTrusted()) {
      throw new Error("Fix with CodeHelper is unavailable in an untrusted workspace");
    }
    const context = await this.#dependencies.captureDiagnostic(snapshot);
    await this.#dependencies.focusChat();
    return this.#dependencies.submit(diagnosticPrompt(action), context);
  }
}
