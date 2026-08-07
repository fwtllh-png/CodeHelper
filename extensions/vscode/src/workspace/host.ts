export interface ExtensionHostEnvironment {
  readonly workspaceScheme: string;
  readonly workspaceAuthority: string;
  readonly storageScheme: string;
}

export function assertWorkspaceExtensionHost(
  environment: ExtensionHostEnvironment,
): void {
  if (environment.storageScheme !== "file") {
    throw new Error(
      "CodeHelper Runtime requires file storage on the local Extension Host",
    );
  }
  if (environment.workspaceScheme !== "file" ||
    environment.workspaceAuthority !== "") {
    throw new Error(
      "CodeHelper VS Code supports only local file workspaces",
    );
  }
}
