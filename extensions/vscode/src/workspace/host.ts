export interface ExtensionHostEnvironment {
  readonly workspaceScheme: string;
  readonly workspaceAuthority: string;
  readonly storageScheme: string;
  readonly remoteName?: string;
}

export function assertWorkspaceExtensionHost(
  environment: ExtensionHostEnvironment,
): void {
  if (environment.storageScheme !== "file") {
    throw new Error(
      "CodeHelper Runtime requires file storage on the Workspace Extension Host",
    );
  }
  if (environment.remoteName === undefined) {
    if (environment.workspaceScheme !== "file" ||
      environment.workspaceAuthority !== "") {
      throw new Error(
        "local Workspace Extension Host requires a local file workspace",
      );
    }
    return;
  }
  const transformedFileWorkspace =
    environment.workspaceScheme === "file" &&
    environment.workspaceAuthority === "";
  if (!validRemoteName(environment.remoteName) ||
    (!transformedFileWorkspace &&
      (environment.workspaceScheme !== "vscode-remote" ||
        !authorityMatchesRemoteName(
          environment.workspaceAuthority,
          environment.remoteName,
        )))) {
    throw new Error(
      "remote workspace authority does not match the Workspace Extension Host",
    );
  }
}

export function authorityMatchesRemoteName(
  authority: string,
  remoteName: string,
): boolean {
  return authority === remoteName || authority.startsWith(`${remoteName}+`);
}

function validRemoteName(value: string): boolean {
  return value.length > 0 &&
    value.length <= 128 &&
    /^[A-Za-z0-9._-]+$/u.test(value);
}
