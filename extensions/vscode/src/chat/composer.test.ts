import assert from "node:assert/strict";
import test from "node:test";

import { projectComposer } from "./composer.js";
import type {
  SessionProfileSnapshot,
  SessionToolCatalog,
} from "../runtime/session.js";

const snapshot: SessionProfileSnapshot = {
  profile: {
    version: 1,
    revision: 4,
    mode: "act",
    provider: "openai",
    model: "gpt-coding",
    reasoning_effort: "high",
    approval_posture: "suggest",
    execution_target: "local",
    max_steps: 8,
    prompt_cache_revision: 3,
  },
  capabilities: {
    provider: "openai",
    model: "gpt-coding",
    model_capabilities: {
      streaming: true,
      reasoning: true,
      tool_calls: true,
      native_search: false,
      vision: false,
      image_input: false,
      prompt_cache: true,
      reasoning_efforts: ["low", "medium", "high"],
    },
    mutable_fields: [
      "mode", "reasoning_effort", "enabled_tool_ids", "approval_posture",
    ],
  },
};

const catalog: SessionToolCatalog = {
  version: 1,
  catalog_id: "catalog-1",
  generation: 2,
  digest: "digest-2",
  tools: [{
    id: "builtin:file_read", name: "file_read", description: "Read files",
    source_kind: "builtin", source_label: "CodeHelper",
    capability: "read", access_mode: "read", sandbox_requirement: "none",
    availability: "available", state: "eager", revision: 1,
    enabled: true, guarded: true,
  }],
};

void test("Composer projects Runtime profile and honest route capabilities", () => {
  const composer = projectComposer(snapshot, catalog, {
    status: "configured",
    provider: "openai",
    source: "secret-storage",
  }, true);

  assert.equal(composer.revision, 4);
  assert.equal(composer.mode.enabled, true);
  assert.equal(composer.provider.enabled, true);
  assert.equal(composer.model.title.includes("restart"), true);
  assert.equal(composer.thinking?.label, "Thinking: High");
  assert.equal(composer.tools.label, "1/1 Tools");
  assert.equal(composer.tools.enabled, true);
  assert.equal(composer.credential.label, "Key configured");
  assert.equal(composer.approval.label, "Suggest");
});

void test("Composer removes escalation controls from untrusted workspaces", () => {
  const composer = projectComposer(snapshot, catalog, {
    status: "missing",
    provider: "openai",
    source: "external",
  }, false);

  assert.equal(composer.provider.enabled, false);
  assert.equal(composer.model.enabled, false);
  assert.equal(composer.credential.enabled, false);
  assert.equal(composer.approval.label, "Read-only");
});
