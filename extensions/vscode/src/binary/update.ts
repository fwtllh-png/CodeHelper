import { readFile } from "node:fs/promises";

import {
  decodeAndVerifyManifest,
  selectArtifact,
} from "./manifest.js";
import type {
  ReleaseArtifact,
  ReleaseManifest,
  TrustRoots,
} from "./manifest.js";
import type { ManagedArtifact, ManagedBinaryStore } from "./store.js";
import { compatibility } from "../compatibility/generated.js";
import { satisfiesRange } from "../compatibility/policy.js";

export const officialReleaseOrigins = [
  "https://github.com",
  "https://release-assets.githubusercontent.com",
] as const;
export const officialManifestURL =
  "https://github.com/fwtllh-png/CodeHelper/releases/latest/download/" +
  "release-manifest.json";

export interface UpdateClientOptions {
  readonly store: ManagedBinaryStore;
  readonly trustRoots: TrustRoots;
  readonly manifestCache?: ManifestCache;
  readonly fetch?: typeof fetch;
  readonly manifestURL?: string;
  readonly origins?: readonly string[];
}

export interface ManifestCacheEntry {
  readonly etag: string;
  readonly bytes: Uint8Array;
}

export interface ManifestCache {
  load(): Promise<ManifestCacheEntry | undefined>;
  save(entry: ManifestCacheEntry): Promise<void>;
}

export class BinaryUpdateClient {
  readonly #store: ManagedBinaryStore;
  readonly #trustRoots: TrustRoots;
  readonly #manifestCache: ManifestCache | undefined;
  readonly #fetch: typeof fetch;
  readonly #manifestURL: string;
  readonly #origins: readonly string[];

  public constructor(options: UpdateClientOptions) {
    this.#store = options.store;
    this.#trustRoots = options.trustRoots;
    this.#manifestCache = options.manifestCache;
    this.#fetch = options.fetch ?? fetch;
    this.#manifestURL = options.manifestURL ?? officialManifestURL;
    this.#origins = options.origins ?? officialReleaseOrigins;
    assertAllowedURL(this.#manifestURL, this.#origins);
  }

  public async check(
    channel: "stable" | "preview",
  ): Promise<{
    readonly manifest: ReleaseManifest;
    readonly artifact: ReleaseArtifact;
  }> {
    const manifestBytes = await this.#downloadManifest();
    const manifest = decodeAndVerifyManifest(
      manifestBytes,
      this.#trustRoots,
    );
    if (manifest.channel !== channel) {
      throw new Error("release manifest channel does not match update channel");
    }
    await this.#store.observeManifest(manifest);
    const artifact = selectArtifact(manifest);
    assertArtifactCompatibility(manifest, artifact);
    assertAllowedURL(artifact.url, this.#origins);
    return { manifest, artifact };
  }

  public async installLatest(
    channel: "stable" | "preview",
    development = false,
  ): Promise<ManagedArtifact> {
    const { manifest, artifact } = await this.check(channel);
    return this.install(manifest, artifact, development);
  }

  public async install(
    manifest: ReleaseManifest,
    artifact: ReleaseArtifact,
    development = false,
  ): Promise<ManagedArtifact> {
    const content = await this.#download(artifact.url, artifact.bytes);
    return this.#store.install(manifest, artifact, content, development);
  }

  async #download(url: string, maximumBytes: number): Promise<Uint8Array> {
    return (await this.#downloadBytes(url, maximumBytes)).bytes;
  }

  async #downloadManifest(): Promise<Uint8Array> {
    const cached = await this.#manifestCache?.load();
    if (cached !== undefined) validateCacheEntry(cached);
    const result = await this.#downloadBytes(
      this.#manifestURL,
      1 << 20,
      cached?.etag,
    );
    if (result.notModified) {
      if (cached === undefined) {
        throw new Error("release manifest returned 304 without a cache entry");
      }
      return cached.bytes;
    }
    if (result.etag !== undefined) {
      await this.#manifestCache?.save({
        etag: result.etag,
        bytes: result.bytes,
      });
    }
    return result.bytes;
  }

  async #downloadBytes(
    url: string,
    maximumBytes: number,
    etag?: string,
  ): Promise<{
    readonly bytes: Uint8Array;
    readonly etag?: string;
    readonly notModified: boolean;
  }> {
    let current = url;
    for (let redirects = 0; redirects <= 3; redirects++) {
      assertAllowedURL(current, this.#origins);
      const response = await this.#fetch(current, {
        method: "GET",
        redirect: "manual",
        signal: AbortSignal.timeout(30_000),
        headers: {
          accept: "application/octet-stream, application/json",
          ...(etag === undefined ? {} : { "if-none-match": etag }),
        },
      });
      if (response.status === 304) {
        return { bytes: new Uint8Array(), notModified: true };
      }
      if (response.status >= 300 && response.status < 400) {
        const location = response.headers.get("location");
        if (location === null || redirects === 3) {
          throw new Error("release download redirect is invalid");
        }
        current = new URL(location, current).toString();
        continue;
      }
      if (!response.ok || response.body === null) {
        throw new Error(`release download failed with HTTP ${String(response.status)}`);
      }
      const length = response.headers.get("content-length");
      if (length !== null && Number(length) > maximumBytes) {
        throw new Error("release download exceeds byte limit");
      }
      const chunks: Uint8Array[] = [];
      let total = 0;
      const reader = response.body.getReader() as
        ReadableStreamDefaultReader<Uint8Array>;
      for (;;) {
        const result = await reader.read();
        if (result.done) break;
        total += result.value.byteLength;
        if (total > maximumBytes) {
          await reader.cancel();
          throw new Error("release download exceeds byte limit");
        }
        chunks.push(result.value);
      }
      const output = new Uint8Array(total);
      let offset = 0;
      for (const chunk of chunks) {
        output.set(chunk, offset);
        offset += chunk.byteLength;
      }
      const responseETag = response.headers.get("etag") ?? undefined;
      if (responseETag !== undefined && !validETag(responseETag)) {
        throw new Error("release download ETag is invalid");
      }
      return {
        bytes: output,
        ...(responseETag === undefined ? {} : { etag: responseETag }),
        notModified: false,
      };
    }
    throw new Error("release download redirect limit exceeded");
  }
}

function validateCacheEntry(entry: ManifestCacheEntry): void {
  if (!validETag(entry.etag) || entry.bytes.byteLength === 0 ||
    entry.bytes.byteLength > (1 << 20)) {
    throw new Error("release manifest cache is invalid");
  }
}

function validETag(value: string): boolean {
  return value.length > 0 && value.length <= 1024 &&
    !value.includes("\r") && !value.includes("\n");
}

export async function readTrustRoots(path: string): Promise<TrustRoots> {
  let value: unknown;
  try {
    value = JSON.parse(await readFile(path, "utf8"));
  } catch (error) {
    throw new Error(`release trust roots are invalid: ${asError(error).message}`);
  }
  if (!isObject(value) || value["schema_version"] !== 1 ||
    !isObject(value["keys"]) ||
    Object.keys(value).some((key) => key !== "schema_version" && key !== "keys")) {
    throw new Error("release trust roots are missing or unsupported");
  }
  const roots: Record<string, string> = {};
  for (const [key, publicKey] of Object.entries(value["keys"])) {
    if (!/^[A-Za-z0-9._-]{1,128}$/u.test(key) ||
      typeof publicKey !== "string" ||
      !publicKey.startsWith("-----BEGIN PUBLIC KEY-----\n")) {
      throw new Error("release trust root entry is invalid");
    }
    roots[key] = publicKey;
  }
  if (Object.keys(roots).length === 0) {
    throw new Error("release trust root set is empty");
  }
  return roots;
}

function assertArtifactCompatibility(
  _manifest: ReleaseManifest,
  artifact: ReleaseManifest["artifacts"][number],
): void {
  if (!satisfiesRange(
    compatibility.extension_version,
    artifact.extensionVersionRange,
  ) ||
    artifact.acpProtocolMin > compatibility.acp_protocol.max ||
    artifact.acpProtocolMax < compatibility.acp_protocol.min ||
    artifact.operationSchemaVersion !== compatibility.operation_schema_version ||
    compatibility.required_features.some(
      (feature) => !artifact.requiredFeatures.includes(feature),
    )) {
    throw new Error("release artifact is incompatible with this extension");
  }
}

function assertAllowedURL(value: string, origins: readonly string[]): void {
  const url = new URL(value);
  if (url.protocol !== "https:" || !origins.includes(url.origin) ||
    url.username !== "" || url.password !== "" || url.hash !== "") {
    throw new Error("release URL is outside the fixed HTTPS origin");
  }
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
