import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeSessionProfileSnapshot,
  decodeSessionProfileUpdate,
} from "./profile.js";

const profile = {
  version: 1,
  revision: 2,
  mode: "act",
  provider: "fixture",
  model: "fixture-model",
  reasoning_effort: "high",
  approval_posture: "suggest",
  execution_target: "local",
  max_steps: 32,
  prompt_cache_revision: 2,
} as const;

void test("session profile decoder preserves revision and capabilities", () => {
  const decoded = decodeSessionProfileSnapshot({
    profile,
    capabilities: {
      provider: "fixture",
      model: "fixture-model",
      model_capabilities: {
        display_name: "Fixture Model",
        context_window: 128_000,
        max_output_tokens: 8_192,
        streaming: true,
        reasoning: true,
        tool_calls: true,
        parallel_tool_calls: "unknown",
        native_search: false,
        vision: false,
        image_input: false,
        prompt_cache: true,
        reasoning_efforts: ["low", "medium", "high"],
        default_reasoning_effort: "high",
        credential_status: "unknown",
        availability: "available",
        selection_mode: "restart_required",
      },
      mutable_fields: [
        "mode",
        "reasoning_effort",
        "approval_posture",
        "max_steps",
      ],
    },
  });
  assert.equal(decoded.profile.revision, 2);
  assert.equal(decoded.capabilities.model_capabilities.prompt_cache, true);
});

void test("session profile decoder rejects drift and route mismatch", () => {
  assert.throws(() => decodeSessionProfileSnapshot({
    profile,
    capabilities: {
      provider: "other",
      model: "fixture-model",
      model_capabilities: {
        display_name: "Fixture Model",
        context_window: 128_000,
        max_output_tokens: 8_192,
        streaming: true,
        reasoning: false,
        tool_calls: false,
        parallel_tool_calls: "unknown",
        native_search: false,
        vision: false,
        image_input: false,
        prompt_cache: false,
        credential_status: "unknown",
        availability: "available",
        selection_mode: "fixed",
      },
      mutable_fields: [],
    },
  }), /do not match/u);
  assert.throws(() => decodeSessionProfileUpdate({
    profile,
    prompt_cache_reset: true,
    forged: true,
  }), /unknown field/u);
});
