import {describe, expect, it} from "vitest";

import {
  emptyModelMetadataDraft,
  modelMetadataProblem
} from "./ModelMetadataFields";

function validDraft() {
  const draft = emptyModelMetadataDraft();
  draft.canonicalID = "vendor/model";
  draft.wireID = "model-1";
  draft.contextTokens = "65536";
  draft.maxOutputTokens = "8192";
  draft.capabilities.streaming = true;
  return draft;
}

describe("modelMetadataProblem", () => {
  it("accepts complete explicit metadata", () => {
    const draft = validDraft();
    draft.capabilities.reasoning = true;
    draft.reasoningEfforts = "off, high, max";
    draft.defaultReasoningEffort = "high";
    expect(modelMetadataProblem(draft, "openai_chat")).toBe("");
  });

  it("validates identity and reasoning declarations", () => {
    const draft = validDraft();
    draft.wireID = "model id";
    expect(modelMetadataProblem(draft, "openai_chat")).toContain(
      "invalid"
    );

    draft.wireID = "model-1";
    draft.capabilities.reasoning = true;
    draft.reasoningEfforts = "high, high";
    expect(modelMetadataProblem(draft, "openai_chat")).toContain("invalid");

    draft.reasoningEfforts = "high";
    draft.defaultReasoningEffort = "max";
    expect(modelMetadataProblem(draft, "openai_chat")).toContain(
      "invalid"
    );
  });

  it("validates capability dependencies and protocol", () => {
    const draft = validDraft();
    draft.capabilities.thinking_toggle = true;
    expect(modelMetadataProblem(draft, "openai_chat")).toContain(
      "invalid"
    );

    draft.capabilities.thinking_toggle = false;
    draft.capabilities.automatic_prompt_cache = true;
    expect(modelMetadataProblem(draft, "openai_chat")).toContain(
      "invalid"
    );

    draft.capabilities.automatic_prompt_cache = false;
    draft.capabilities.incremental_responses = true;
    expect(modelMetadataProblem(draft, "openai_chat")).toContain(
      "invalid"
    );
    expect(modelMetadataProblem(draft, "openai_responses")).toBe("");
  });
});
