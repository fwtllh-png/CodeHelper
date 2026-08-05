export type RuntimePosture = "suggest" | "never";

export interface ScopedConfiguration<T> {
  readonly globalValue?: T;
  readonly workspaceValue?: T;
  readonly workspaceFolderValue?: T;
}

export function runtimePosture(workspaceTrusted: boolean): RuntimePosture {
  return workspaceTrusted ? "suggest" : "never";
}

// Workspace settings are repository-controlled input. An untrusted repository
// must not be able to select the executable that the extension launches.
export function configuredBinaryPath(
  values: ScopedConfiguration<string> | undefined,
  workspaceTrusted: boolean,
): string | undefined {
  if (values === undefined) {
    return undefined;
  }
  const selected = workspaceTrusted
    ? values.workspaceFolderValue ?? values.workspaceValue ?? values.globalValue
    : values.globalValue;
  const trimmed = selected?.trim();
  return trimmed === "" ? undefined : trimmed;
}
