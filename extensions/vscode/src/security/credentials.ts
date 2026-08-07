import { createHash } from "node:crypto";

export type CredentialStatus = "configured" | "missing" | "invalid";

interface SecretStorage {
  get(key: string): PromiseLike<string | undefined>;
  store(key: string, value: string): PromiseLike<void>;
  delete(key: string): PromiseLike<void>;
}

export interface CredentialView {
  readonly status: CredentialStatus;
  readonly provider: string;
  readonly source: "secret-storage" | "external";
  readonly validation: "not_validated" | "valid" | "invalid";
  readonly validatedAt?: string;
  readonly validationFailure?: "authentication" | "network" | "provider" | "unknown";
}

const providerPattern = /^[a-z0-9][a-z0-9._-]{0,127}$/u;
const rootPattern = /^[0-9a-f]{64}$/u;

export class WorkspaceCredentialStore {
  readonly #secrets: SecretStorage;
  readonly #rootId: string;

  public constructor(secrets: SecretStorage, rootId: string) {
    if (!rootPattern.test(rootId)) {
      throw new Error("credential store requires a workspace root identity");
    }
    this.#secrets = secrets;
    this.#rootId = rootId;
  }

  public reference(provider: string): string {
    requireProvider(provider);
    const digest = createHash("sha256")
      .update(`${this.#rootId}\0${provider}`)
      .digest("hex")
      .slice(0, 24)
      .toUpperCase();
    return `CODEHELPER_VSCODE_CREDENTIAL_${digest}`;
  }

  public async status(
    provider: string,
    externalReference = false,
  ): Promise<CredentialView> {
    requireProvider(provider);
    let secret: string | undefined;
    try {
      secret = await this.#secrets.get(this.#key(provider));
    } catch {
      return {
        status: "invalid",
        provider,
        source: "secret-storage",
        validation: "not_validated",
      };
    }
    if (secret !== undefined && secret.trim().length > 0) {
      return {
        status: "configured",
        provider,
        source: "secret-storage",
        validation: "not_validated",
      };
    }
    return {
      status: externalReference ? "configured" : "missing",
      provider,
      source: "external",
      validation: "not_validated",
    };
  }

  public async store(provider: string, secret: string): Promise<void> {
    requireProvider(provider);
    if (secret.trim().length === 0 || secret.length > 32 << 10) {
      throw new Error("credential secret is invalid");
    }
    await this.#secrets.store(this.#key(provider), secret);
  }

  public async delete(provider: string): Promise<void> {
    requireProvider(provider);
    await this.#secrets.delete(this.#key(provider));
  }

  public async environment(
    provider: string,
  ): Promise<Readonly<Record<string, string>>> {
    requireProvider(provider);
    const secret = await this.#secrets.get(this.#key(provider));
    if (secret === undefined || secret.trim().length === 0) {
      return {};
    }
    return { [this.reference(provider)]: secret };
  }

  #key(provider: string): string {
    return `codehelper.credential.v1:${this.#rootId}:${provider}`;
  }
}

function requireProvider(provider: string): void {
  if (!providerPattern.test(provider)) {
    throw new Error("credential provider is invalid");
  }
}
