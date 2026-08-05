import { createHash } from "node:crypto";
import { constants } from "node:fs";
import { access, lstat, readFile } from "node:fs/promises";
import { join } from "node:path";

import {
  ManagedBinaryStore,
  type BinarySource,
} from "./store.js";
import {
  decodeAndVerifyManifest,
  selectArtifact,
} from "./manifest.js";
import { readTrustRoots } from "./update.js";
import { discoverBinary } from "../runtime/process.js";

export interface BinarySourceOptions {
  readonly source: BinarySource;
  readonly configuredPath?: string;
  readonly developmentRoot?: string;
  readonly extensionPath: string;
  readonly storageRoot?: string;
}

export interface ResolvedBinary {
  readonly path: string;
  readonly source: Exclude<BinarySource, "auto">;
  readonly managedDigest?: string;
  readonly managedStore?: ManagedBinaryStore;
}

export async function resolveBinarySource(
  options: BinarySourceOptions,
): Promise<ResolvedBinary> {
  if (options.source === "bundled" || options.source === "auto") {
    const bundled = bundledBinaryPath(options.extensionPath);
    if (await validBundledBinary(options.extensionPath, bundled)) {
      return { path: bundled, source: "bundled" };
    }
    if (options.source === "bundled") {
      throw new Error("bundled CodeHelper binary is unavailable for this target");
    }
  }
  if (options.source === "managed" || options.source === "auto") {
    if (options.storageRoot !== undefined) {
      const store = new ManagedBinaryStore(
        join(options.storageRoot, "managed-binary"),
      );
      const managed = await store.resolve();
      if (managed !== undefined) {
        return {
          path: managed.path,
          source: "managed",
          managedDigest: managed.digest,
          managedStore: store,
        };
      }
    }
    if (options.source === "managed") {
      throw new Error(
        options.storageRoot === undefined
          ? "managed CodeHelper binary requires Host-local file storage"
          : "managed CodeHelper binary is not installed; run CodeHelper: Check for Binary Updates",
      );
    }
  }
  return {
    path: await discoverBinary({
      ...(options.configuredPath === undefined
        ? {}
        : { configuredPath: options.configuredPath }),
      ...(options.developmentRoot === undefined
        ? {}
        : { developmentRoot: options.developmentRoot }),
    }),
    source: "external",
  };
}

async function validBundledBinary(
  extensionPath: string,
  binaryPath: string,
): Promise<boolean> {
  try {
    if (!await executable(binaryPath)) return false;
    const manifestBytes = await readFile(
      join(extensionPath, "bin", "release-manifest.json"),
    );
    const roots = await readTrustRoots(join(
      extensionPath,
      "resources",
      "release-trust-roots.json",
    ));
    const artifact = selectArtifact(
      decodeAndVerifyManifest(manifestBytes, roots),
    );
    const info = await lstat(binaryPath);
    if (!info.isFile() || info.isSymbolicLink() || info.nlink !== 1 ||
      info.size !== artifact.bytes) {
      return false;
    }
    return createHash("sha256")
      .update(await readFile(binaryPath))
      .digest("hex") === artifact.sha256;
  } catch {
    return false;
  }
}

export function bundledBinaryPath(
  extensionPath: string,
  platform = process.platform,
  architecture = process.arch,
): string {
  const os = platform === "win32" ? "win32" : platform;
  const arch = architecture === "x64" ? "x64" : architecture;
  const executable = platform === "win32" ? "codehelper.exe" : "codehelper";
  return join(extensionPath, "bin", `${os}-${arch}`, executable);
}

async function executable(path: string): Promise<boolean> {
  try {
    await access(
      path,
      process.platform === "win32" ? constants.F_OK : constants.X_OK,
    );
    return true;
  } catch {
    return false;
  }
}
