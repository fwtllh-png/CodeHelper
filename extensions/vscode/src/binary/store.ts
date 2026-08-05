import { createHash, randomUUID } from "node:crypto";
import {
  chmod,
  lstat,
  mkdir,
  open,
  readFile,
  rename,
  rm,
  stat,
  statfs,
} from "node:fs/promises";
import { dirname, extname, join } from "node:path";

import type {
  ReleaseArtifact,
  ReleaseManifest,
} from "./manifest.js";
import { assertCompatibleBinary } from "../compatibility/policy.js";
import {
  verifyBinary,
  type BinaryVersion,
} from "../runtime/process.js";

export type BinarySource = "auto" | "external" | "managed" | "bundled";

export interface ManagedArtifact {
  readonly digest: string;
  readonly version: string;
  readonly path: string;
  readonly sequence: number;
}

interface StoredArtifact {
  readonly digest: string;
  readonly version: string;
  readonly sequence: number;
}

interface UpdateReceipt {
  readonly sequence: number;
  readonly version: string;
  readonly digest: string;
  readonly result: "pending" | "healthy" | "rollback";
  readonly at: string;
}

interface BinaryState {
  readonly schema_version: 1;
  readonly max_sequence: number;
  readonly active?: StoredArtifact;
  readonly last_known_good?: StoredArtifact;
  readonly pending_digest?: string;
  readonly revoked_versions: readonly string[];
  readonly revoked_digests: readonly string[];
  readonly receipts: readonly UpdateReceipt[];
}

export interface ManagedBinaryStoreOptions {
  readonly verify?: (path: string) => Promise<BinaryVersion>;
  readonly now?: () => Date;
  readonly platform?: NodeJS.Platform;
  readonly architecture?: NodeJS.Architecture;
  readonly availableBytes?: (path: string) => Promise<number>;
}

const maxArtifactBytes = 256 << 20;
const maxStateBytes = 1 << 20;

export class ManagedBinaryStore {
  readonly #root: string;
  readonly #verify: (path: string) => Promise<BinaryVersion>;
  readonly #now: () => Date;
  readonly #platform: NodeJS.Platform;
  readonly #architecture: NodeJS.Architecture;
  readonly #availableBytes: (path: string) => Promise<number>;

  public constructor(root: string, options: ManagedBinaryStoreOptions = {}) {
    this.#root = root;
    this.#verify = options.verify ?? verifyBinary;
    this.#now = options.now ?? (() => new Date());
    this.#platform = options.platform ?? process.platform;
    this.#architecture = options.architecture ?? process.arch;
    this.#availableBytes = options.availableBytes ?? availableDiskBytes;
  }

  public async resolve(): Promise<ManagedArtifact | undefined> {
    return this.#withLock(async () => {
      const state = await this.#readState();
      const active = state.active;
      if (active === undefined) return undefined;
      this.#assertNotRevoked(state, active);
      const path = this.#artifactPath(active.digest);
      await verifyStoredArtifact(path, active.digest);
      return { ...active, path };
    });
  }

  public async install(
    manifest: ReleaseManifest,
    artifact: ReleaseArtifact,
    content: Uint8Array,
    development = false,
  ): Promise<ManagedArtifact> {
    return this.#withLock(async () => {
      const state = await this.#readState();
      if (manifest.sequence < state.max_sequence ||
        state.active?.sequence === manifest.sequence) {
        throw new Error("release manifest sequence is not monotonic");
      }
      if (content.byteLength !== artifact.bytes ||
        content.byteLength === 0 ||
        content.byteLength > maxArtifactBytes) {
        throw new Error("release artifact size does not match manifest");
      }
      const requiredBytes = content.byteLength * 2 + (16 << 20);
      if (await this.#availableBytes(this.#root) < requiredBytes) {
        throw new Error("managed binary update has insufficient disk space");
      }
      const digest = sha256(content);
      if (digest !== artifact.sha256) {
        throw new Error("release artifact digest does not match manifest");
      }
      const staged = join(this.#root, `.stage-${randomUUID()}`);
      await writeExclusive(staged, content, this.#platform);
      try {
        const version = await this.#verify(staged);
        assertArtifactIdentity(version, artifact);
        assertCompatibleBinary(
          version,
          development,
          this.#platform,
          this.#architecture,
        );
        const target = this.#artifactPath(digest);
        await mkdir(dirname(target), { recursive: true, mode: 0o700 });
        try {
          await rename(staged, target);
        } catch (error) {
          if (!await regularFileExists(target)) throw error;
          await verifyStoredArtifact(target, digest);
        }
        const installed: StoredArtifact = {
          digest,
          version: artifact.version,
          sequence: manifest.sequence,
        };
        const next = normalizeState({
          ...state,
          max_sequence: manifest.sequence,
          active: installed,
          ...(state.active === undefined
            ? {}
            : { last_known_good: state.active }),
          pending_digest: digest,
          revoked_versions: manifest.revokedVersions,
          revoked_digests: manifest.revokedDigests,
          receipts: appendReceipt(state.receipts, {
            sequence: manifest.sequence,
            version: artifact.version,
            digest,
            result: "pending",
            at: this.#now().toISOString(),
          }),
        });
        this.#assertNotRevoked(next, installed);
        await this.#writeState(next);
        return { ...installed, path: target };
      } finally {
        await rm(staged, { force: true });
      }
    });
  }

  public async observeManifest(manifest: ReleaseManifest): Promise<void> {
    await this.#withLock(async () => {
      const state = await this.#readState();
      if (manifest.sequence < state.max_sequence) {
        throw new Error("release manifest sequence is not monotonic");
      }
      await this.#writeState(normalizeState({
        ...state,
        max_sequence: manifest.sequence,
        revoked_versions: manifest.revokedVersions,
        revoked_digests: manifest.revokedDigests,
      }));
    });
  }

  public async markHealthy(digest: string): Promise<void> {
    await this.#withLock(async () => {
      const state = await this.#readState();
      if (state.pending_digest !== digest || state.active?.digest !== digest) {
        return;
      }
      await this.#writeState(normalizeState({
        schema_version: 1,
        max_sequence: state.max_sequence,
        active: state.active,
        ...(state.last_known_good === undefined
          ? {}
          : { last_known_good: state.last_known_good }),
        revoked_versions: state.revoked_versions,
        revoked_digests: state.revoked_digests,
        receipts: appendReceipt(state.receipts, {
          sequence: state.active.sequence,
          version: state.active.version,
          digest,
          result: "healthy",
          at: this.#now().toISOString(),
        }),
      }));
    });
  }

  public async rollbackPending(): Promise<boolean> {
    return this.#withLock(async () => {
      const state = await this.#readState();
      if (state.pending_digest === undefined) return false;
      const fallback = state.last_known_good;
      if (fallback === undefined) {
        await this.#writeState(normalizeState({
          schema_version: 1,
          max_sequence: state.max_sequence,
          revoked_versions: state.revoked_versions,
          revoked_digests: state.revoked_digests,
          receipts: state.receipts,
        }));
        return true;
      }
      if (state.revoked_versions.includes(fallback.version) ||
        state.revoked_digests.includes(fallback.digest)) {
        await this.#writeState(normalizeState({
          schema_version: 1,
          max_sequence: state.max_sequence,
          revoked_versions: state.revoked_versions,
          revoked_digests: state.revoked_digests,
          receipts: state.receipts,
        }));
        return true;
      }
      await verifyStoredArtifact(
        this.#artifactPath(fallback.digest),
        fallback.digest,
      );
      const failed = state.active;
      await this.#writeState(normalizeState({
        schema_version: 1,
        max_sequence: state.max_sequence,
        active: fallback,
        revoked_versions: state.revoked_versions,
        revoked_digests: state.revoked_digests,
        receipts: failed === undefined
          ? state.receipts
          : appendReceipt(state.receipts, {
              sequence: failed.sequence,
              version: failed.version,
              digest: failed.digest,
              result: "rollback",
              at: this.#now().toISOString(),
            }),
      }));
      return true;
    });
  }

  #artifactPath(digest: string): string {
    const executable = this.#platform === "win32" ? "codehelper.exe" : "codehelper";
    return join(this.#root, "artifacts", digest, executable);
  }

  async #withLock<T>(operation: () => Promise<T>): Promise<T> {
    await ensureSafeRoot(this.#root);
    const lock = join(this.#root, ".update-lock");
    const deadline = Date.now() + 10_000;
    for (;;) {
      try {
        await mkdir(lock, { mode: 0o700 });
        break;
      } catch (error) {
        if (!isErrorCode(error, "EEXIST") || Date.now() >= deadline) {
          throw new Error("managed binary update lock is unavailable");
        }
        const info = await stat(lock).catch(() => undefined);
        if (info !== undefined && Date.now() - info.mtimeMs > 120_000) {
          await rm(lock, { recursive: true, force: true });
        } else {
          await delay(50);
        }
      }
    }
    try {
      return await operation();
    } finally {
      await rm(lock, { recursive: true, force: true });
    }
  }

  async #readState(): Promise<BinaryState> {
    const path = join(this.#root, "state.json");
    const info = await lstat(path).catch((error: unknown) => {
      if (isErrorCode(error, "ENOENT")) return undefined;
      throw error;
    });
    if (info === undefined) return emptyState();
    if (!info.isFile() || info.isSymbolicLink() || info.nlink !== 1 ||
      info.size > maxStateBytes) {
      throw new Error("managed binary state is unsafe");
    }
    let raw: unknown;
    try {
      raw = JSON.parse(await readFile(path, "utf8"));
    } catch (error) {
      throw new Error(`managed binary state is invalid: ${asError(error).message}`);
    }
    return decodeState(raw);
  }

  async #writeState(state: BinaryState): Promise<void> {
    const path = join(this.#root, "state.json");
    const temporary = join(this.#root, `.state-${randomUUID()}`);
    const handle = await open(temporary, "wx", 0o600);
    try {
      await handle.writeFile(JSON.stringify(state));
      await handle.sync();
    } finally {
      await handle.close();
    }
    await rename(temporary, path);
  }

  #assertNotRevoked(state: BinaryState, artifact: StoredArtifact): void {
    if (state.revoked_versions.includes(artifact.version) ||
      state.revoked_digests.includes(artifact.digest)) {
      throw new Error("managed CodeHelper binary is revoked");
    }
  }
}

async function availableDiskBytes(path: string): Promise<number> {
  const value = await statfs(path);
  return value.bavail * value.bsize;
}

function emptyState(): BinaryState {
  return {
    schema_version: 1,
    max_sequence: 0,
    revoked_versions: [],
    revoked_digests: [],
    receipts: [],
  };
}

function normalizeState(value: {
  readonly schema_version: 1;
  readonly max_sequence: number;
  readonly active?: StoredArtifact;
  readonly last_known_good?: StoredArtifact;
  readonly pending_digest?: string;
  readonly revoked_versions: readonly string[];
  readonly revoked_digests: readonly string[];
  readonly receipts: readonly UpdateReceipt[];
}): BinaryState {
  return {
    schema_version: 1,
    max_sequence: value.max_sequence,
    ...(value.active === undefined ? {} : { active: value.active }),
    ...(value.last_known_good === undefined
      ? {}
      : { last_known_good: value.last_known_good }),
    ...(value.pending_digest === undefined
      ? {}
      : { pending_digest: value.pending_digest }),
    revoked_versions: value.revoked_versions,
    revoked_digests: value.revoked_digests,
    receipts: value.receipts,
  };
}

function decodeState(value: unknown): BinaryState {
  if (!isObject(value) || value["schema_version"] !== 1 ||
    !Number.isSafeInteger(value["max_sequence"]) ||
    (value["max_sequence"] as number) < 0 ||
    !Array.isArray(value["revoked_versions"]) ||
    !Array.isArray(value["revoked_digests"]) ||
    !Array.isArray(value["receipts"])) {
    throw new Error("managed binary state is missing or unsupported");
  }
  const active = decodeStored(value["active"]);
  const lastKnownGood = decodeStored(value["last_known_good"]);
  const pending = value["pending_digest"];
  if (pending !== undefined && !validDigest(pending)) {
    throw new Error("managed binary pending digest is invalid");
  }
  return normalizeState({
    schema_version: 1,
    max_sequence: value["max_sequence"] as number,
    ...(active === undefined ? {} : { active }),
    ...(lastKnownGood === undefined ? {} : { last_known_good: lastKnownGood }),
    ...(pending === undefined ? {} : { pending_digest: pending }),
    revoked_versions: decodeStrings(value["revoked_versions"]),
    revoked_digests: decodeStrings(value["revoked_digests"], true),
    receipts: (value["receipts"] as unknown[]).slice(-64)
      .map(decodeReceipt),
  });
}

function decodeStored(value: unknown): StoredArtifact | undefined {
  if (value === undefined) return undefined;
  if (!isObject(value) || !validDigest(value["digest"]) ||
    typeof value["version"] !== "string" ||
    !Number.isSafeInteger(value["sequence"]) ||
    (value["sequence"] as number) <= 0) {
    throw new Error("managed binary state artifact is invalid");
  }
  return {
    digest: value["digest"],
    version: value["version"],
    sequence: value["sequence"] as number,
  };
}

function decodeReceipt(value: unknown): UpdateReceipt {
  if (!isObject(value) || !validDigest(value["digest"]) ||
    typeof value["version"] !== "string" ||
    !Number.isSafeInteger(value["sequence"]) ||
    typeof value["at"] !== "string" ||
    (value["result"] !== "pending" &&
      value["result"] !== "healthy" &&
      value["result"] !== "rollback")) {
    throw new Error("managed binary receipt is invalid");
  }
  return {
    sequence: value["sequence"] as number,
    version: value["version"],
    digest: value["digest"],
    result: value["result"],
    at: value["at"],
  };
}

function decodeStrings(value: unknown, digest = false): readonly string[] {
  if (!Array.isArray(value) || value.length > 128 ||
    value.some((item) =>
      typeof item !== "string" || (digest && !validDigest(item)))) {
    throw new Error("managed binary state revocations are invalid");
  }
  return value as string[];
}

function appendReceipt(
  values: readonly UpdateReceipt[],
  value: UpdateReceipt,
): readonly UpdateReceipt[] {
  return [...values, value].slice(-64);
}

function assertArtifactIdentity(
  binary: BinaryVersion,
  artifact: ReleaseArtifact,
): void {
  if (binary.version !== artifact.version ||
    binary.commit !== artifact.commit ||
    binary.os !== artifact.target.os ||
    binary.arch !== artifact.target.arch ||
    binary.acpProtocolMin !== artifact.acpProtocolMin ||
    binary.acpProtocolMax !== artifact.acpProtocolMax ||
    binary.operationSchemaVersion !== artifact.operationSchemaVersion) {
    throw new Error("release artifact identity does not match manifest");
  }
}

async function ensureSafeRoot(root: string): Promise<void> {
  await mkdir(root, { recursive: true, mode: 0o700 });
  const info = await lstat(root);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("managed binary storage root is unsafe");
  }
}

async function writeExclusive(
  path: string,
  content: Uint8Array,
  platform: NodeJS.Platform,
): Promise<void> {
  const handle = await open(path, "wx", 0o600);
  try {
    await handle.writeFile(content);
    await handle.sync();
  } finally {
    await handle.close();
  }
  if (platform !== "win32" && extname(path).toLowerCase() !== ".cmd") {
    await chmod(path, 0o500);
  }
}

async function verifyStoredArtifact(path: string, digest: string): Promise<void> {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink() || info.nlink !== 1 ||
    info.size <= 0 || info.size > maxArtifactBytes) {
    throw new Error("managed binary artifact is unsafe");
  }
  const content = await readFile(path);
  if (sha256(content) !== digest) {
    throw new Error("managed binary artifact is corrupt");
  }
}

async function regularFileExists(path: string): Promise<boolean> {
  const info = await lstat(path).catch(() => undefined);
  return info?.isFile() === true && !info.isSymbolicLink() && info.nlink === 1;
}

function sha256(value: Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}

function validDigest(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{64}$/u.test(value);
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isErrorCode(value: unknown, code: string): boolean {
  return isObject(value) && value["code"] === code;
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, milliseconds);
  });
}
