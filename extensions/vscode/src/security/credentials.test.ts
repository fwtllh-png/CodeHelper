import assert from "node:assert/strict";
import test from "node:test";

import { WorkspaceCredentialStore } from "./credentials.js";

class MemorySecrets {
  readonly values = new Map<string, string>();

  public get(key: string): Promise<string | undefined> {
    return Promise.resolve(this.values.get(key));
  }

  public store(key: string, value: string): Promise<void> {
    this.values.set(key, value);
    return Promise.resolve();
  }

  public delete(key: string): Promise<void> {
    this.values.delete(key);
    return Promise.resolve();
  }
}

void test("WorkspaceCredentialStore exposes status without leaking secrets", async () => {
  const secrets = new MemorySecrets();
  const store = new WorkspaceCredentialStore(secrets, "a".repeat(64));
  const secret = "credential-value-that-must-not-leak";

  assert.equal((await store.status("openai")).status, "missing");
  await store.store("openai", secret);
  const status = await store.status("openai");
  assert.deepEqual(status, {
    status: "configured",
    provider: "openai",
    source: "secret-storage",
  });
  assert.equal(JSON.stringify(status).includes(secret), false);

  const environment = await store.environment("openai");
  assert.equal(Object.values(environment)[0], secret);
  assert.match(Object.keys(environment)[0] ?? "", /^CODEHELPER_VSCODE_CREDENTIAL_/u);
  assert.equal(JSON.stringify(status).includes(Object.keys(environment)[0] ?? ""), false);
});

void test("WorkspaceCredentialStore rejects empty secrets and forged providers", async () => {
  const store = new WorkspaceCredentialStore(
    new MemorySecrets(),
    "b".repeat(64),
  );
  await assert.rejects(store.store("openai", " "), /secret is invalid/u);
  await assert.rejects(store.store("../../escape", "secret"), /provider is invalid/u);
});

void test("WorkspaceCredentialStore projects SecretStorage failures as invalid", async () => {
  const store = new WorkspaceCredentialStore({
    get: () => Promise.reject(new Error("locked")),
    store: () => Promise.resolve(),
    delete: () => Promise.resolve(),
  }, "c".repeat(64));
  assert.equal((await store.status("openai")).status, "invalid");
});
