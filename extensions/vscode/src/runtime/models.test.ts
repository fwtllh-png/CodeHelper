import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeModelCatalog,
  decodeProviderCatalog,
} from "./models.js";

const capabilities = {
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
  reasoning_efforts: ["low", "high"],
  default_reasoning_effort: "low",
  credential_status: "unknown",
  availability: "available",
  selection_mode: "restart_required",
};

void test("model catalogs preserve availability and restart requirements", () => {
  const providers = decodeProviderCatalog({
    version: 1,
    providers: [{
      id: "fixture",
      display_name: "Fixture",
      selected: true,
      availability: "available",
    }],
  });
  const models = decodeModelCatalog({
    version: 1,
    models: [{
      provider: "fixture",
      id: "fixture-model",
      selected: true,
      capabilities,
    }],
  });
  assert.equal(providers.providers[0]?.selected, true);
  assert.equal(
    models.models[0]?.capabilities.selection_mode,
    "restart_required",
  );
});

void test("model catalogs reject drift and unavailable entries without reasons", () => {
  assert.throws(() => decodeProviderCatalog({
    version: 1,
    providers: [{
      id: "fixture",
      display_name: "Fixture",
      selected: false,
      availability: "unavailable",
    }],
  }), /requires a reason/u);
  assert.throws(() => decodeModelCatalog({
    version: 1,
    models: [{
      provider: "fixture",
      id: "fixture-model",
      selected: true,
      capabilities: { ...capabilities, selection_mode: "magic" },
    }],
  }), /selection mode is invalid/u);
});
