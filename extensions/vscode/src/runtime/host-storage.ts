import { mkdir, stat } from "node:fs/promises";
import { isAbsolute, resolve } from "node:path";

export interface HostLocalStorageEnvironment {
  readonly uiExtensionHost: boolean;
  readonly remoteName: string | undefined;
  readonly scheme: string;
  readonly fsPath: string;
}

export async function hostLocalStoragePath(
  environment: HostLocalStorageEnvironment,
): Promise<string> {
  if (!environment.uiExtensionHost || environment.remoteName !== undefined) {
    throw new Error("binary storage requires the local UI Extension Host");
  }
  if (environment.scheme !== "file" &&
    environment.scheme !== "vscode-userdata") {
    throw new Error(
      `binary storage URI scheme ${environment.scheme} is not Host-local`,
    );
  }
  if (!isAbsolute(environment.fsPath)) {
    throw new Error("binary storage requires an absolute Host-local path");
  }
  const path = resolve(environment.fsPath);
  await mkdir(path, { recursive: true, mode: 0o700 });
  if (!(await stat(path)).isDirectory()) {
    throw new Error("binary storage path is not a directory");
  }
  return path;
}
