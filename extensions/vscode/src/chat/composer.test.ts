import assert from "node:assert/strict";
import test from "node:test";

import { projectComposer } from "./composer.js";
import type { SessionProfileSnapshot } from "../runtime/session.js";

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
    mutable_fields: ["mode", "reasoning_effort", "approval_posture"],
  },
};

void test("Composer projects Runtime profile and honest route capabilities", () => {
  const composer = projectComposer(snapshot, {
    status: "configured",
    provider: "openai",
    source: "secret-storage",
  }, true);

  assert.equal(composer.revision, 4);
  assert.equal(composer.mode.enabled, true);
  assert.equal(composer.provider.enabled, true);
  assert.equal(composer.model.title.includes("restart"), true);
  assert.equal(composer.thinking?.label, "Thinking: High");
  assert.equal(composer.credential.label, "Key configured");
  assert.equal(composer.approval.label, "Suggest");
});

void test("Composer removes escalation controls from untrusted workspaces", () => {
  const composer = projectComposer(snapshot, {
    status: "missing",
    provider: "openai",
    source: "external",
  }, false);

  assert.equal(composer.provider.enabled, false);
  assert.equal(composer.model.enabled, false);
  assert.equal(composer.credential.enabled, false);
  assert.equal(composer.approval.label, "Read-only");
});
