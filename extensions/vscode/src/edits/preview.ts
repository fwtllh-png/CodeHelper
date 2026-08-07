import * as vscode from "vscode";

import type { EditPlanCard, EditPlanFileCard } from "./model.js";

const previewScheme = "codehelper-edit-plan";
const maxPreviewDocuments = 256;
const maxPreviewBytes = 16 << 20;

interface PreviewDocument {
  readonly content: string;
  readonly bytes: number;
}

export class EditPlanPreview implements vscode.TextDocumentContentProvider, vscode.Disposable {
  readonly #documents = new Map<string, PreviewDocument>();
  readonly #change = new vscode.EventEmitter<vscode.Uri>();
  readonly #registration: vscode.Disposable;
  #documentBytes = 0;

  public readonly onDidChange = this.#change.event;

  public constructor() {
    this.#registration = vscode.workspace.registerTextDocumentContentProvider(
      previewScheme,
      this,
    );
  }

  public provideTextDocumentContent(uri: vscode.Uri): string {
    return this.#documents.get(uri.toString())?.content ?? "";
  }

  public async show(
    plan: EditPlanCard,
    rootId?: string,
  ): Promise<void> {
    if (plan.files.length === 0) {
      throw new Error("edit plan contains no files");
    }
    for (const index of plan.files.keys()) {
      await this.showFile(
        plan,
        index,
        index !== plan.files.length - 1,
        rootId,
      );
    }
  }

  public async showFile(
    plan: EditPlanCard,
    fileIndex: number,
    preview = false,
    rootId?: string,
    side = false,
  ): Promise<void> {
    if (!Number.isSafeInteger(fileIndex) ||
      fileIndex < 0 || fileIndex >= plan.files.length) {
      throw new Error("edit plan file is unknown");
    }
    const file = plan.files[fileIndex];
    if (file === undefined) {
      throw new Error("edit plan file is unknown");
    }
    const authority = previewAuthority(plan.id, rootId);
    const before = this.#uri(authority, fileIndex, "before", file);
    const after = this.#uri(authority, fileIndex, "after", file);
    this.#set(before, file.before);
    this.#set(after, file.after);
    await vscode.commands.executeCommand(
      "vscode.diff",
      before,
      after,
      `CodeHelper Plan: ${file.path} (${file.kind})`,
      {
        preview,
        ...(side ? { viewColumn: vscode.ViewColumn.Beside } : {}),
      },
    );
  }

  public dispose(): void {
    this.#registration.dispose();
    this.#change.dispose();
    this.#documents.clear();
    this.#documentBytes = 0;
  }

  #set(uri: vscode.Uri, content: string): void {
    const key = uri.toString();
    const previous = this.#documents.get(key);
    if (previous !== undefined) {
      this.#documentBytes -= previous.bytes;
      this.#documents.delete(key);
    }
    const document = {
      content,
      bytes: Buffer.byteLength(content, "utf8"),
    };
    this.#documents.set(key, document);
    this.#documentBytes += document.bytes;
    while (this.#documents.size > maxPreviewDocuments ||
      this.#documentBytes > maxPreviewBytes) {
      const oldest = this.#documents.entries().next().value;
      if (oldest === undefined || oldest[0] === key) {
        break;
      }
      this.#documents.delete(oldest[0]);
      this.#documentBytes -= oldest[1].bytes;
    }
    this.#change.fire(uri);
  }

  #uri(
    planId: string,
    index: number,
    side: "before" | "after",
    file: EditPlanFileCard,
  ): vscode.Uri {
    const suffix = file.path.split(".").at(-1) ?? "";
    const extension = /^[a-zA-Z0-9]{1,16}$/u.test(suffix)
      ? `.${suffix}`
      : ".txt";
    return vscode.Uri.from({
      scheme: previewScheme,
      authority: planId,
      path: `/${String(index)}-${side}${extension}`,
      query: `path=${encodeURIComponent(file.path)}`,
    });
  }
}

function previewAuthority(planId: string, rootId?: string): string {
  if (rootId === undefined) return planId;
  if (!/^[0-9a-f]{64}$/u.test(rootId)) {
    throw new Error("edit plan workspace root is invalid");
  }
  return `${rootId}-${planId}`;
}
