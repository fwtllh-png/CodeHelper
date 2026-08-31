import {describe, expect, it} from "vitest";

import {
  emptyModelMetadataDraft,
  modelMetadataForModel,
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
    expect(modelMetadataProblem(draft, "openai_chat")).toBe(
      "Enable prompt cache first."
    );

    draft.capabilities.automatic_prompt_cache = false;
    draft.capabilities.incremental_responses = true;
    expect(modelMetadataProblem(draft, "openai_chat")).toBe(
      "Incremental responses require Responses."
    );
    expect(modelMetadataProblem(draft, "openai_responses")).toBe("");
  });

  it("keeps model identities synchronized until explicitly overridden", () => {
    const initial = modelMetadataForModel(
      emptyModelMetadataDraft(),
      "",
      "model-v1"
    );
    expect(initial.canonicalID).toBe("model-v1");
    expect(initial.wireID).toBe("model-v1");
    expect(initial.capabilities.streaming).toBe(true);

    initial.canonicalID = "vendor/model-v1";
    const changed = modelMetadataForModel(initial, "model-v1", "model-v2");
    expect(changed.canonicalID).toBe("vendor/model-v1");
    expect(changed.wireID).toBe("model-v2");
  });

  it("reports the invalid metadata field group", () => {
    const draft = emptyModelMetadataDraft("model-v1");
    expect(modelMetadataProblem(draft, "openai_chat")).toBe(
      "Enter token limits."
    );
    draft.contextTokens = "1024";
    draft.maxOutputTokens = "2048";
    expect(modelMetadataProblem(draft, "openai_chat")).toBe(
      "Output exceeds context."
    );
  });
});
