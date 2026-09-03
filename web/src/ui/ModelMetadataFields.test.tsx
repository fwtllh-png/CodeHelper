import {render, screen} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {
  emptyModelMetadataDraft,
  ModelMetadataFields,
  modelMetadataFromProbe,
  modelMetadataProblem
} from "./ModelMetadataFields";

function validDraft() {
  const draft = emptyModelMetadataDraft();
  draft.canonicalID = "vendor/model";
  draft.wireID = "model-1";
  draft.contextTokens = "65536";
  draft.maxOutputTokens = "8192";
  draft.capabilities.streaming = true;
  draft.capabilities.tool_calls = true;
  return draft;
}

describe("modelMetadataProblem", () => {
  it("preserves reasoning effort metadata returned by probing", () => {
    const draft = modelMetadataFromProbe("reasoner", {
      capabilities: {
        streaming: true,
        reasoning: true,
        reasoning_efforts: ["low", "medium", "high"],
        default_reasoning_effort: "medium",
        tool_calls: true,
        native_search: false,
        vision: false,
        image_input: false,
        prompt_cache: false
      }
    });
    expect(draft.reasoningEfforts).toBe("low, medium, high");
    expect(draft.defaultReasoningEffort).toBe("medium");
  });

  it("shows explicit effort metadata for reasoning models", () => {
    const draft = validDraft();
    draft.capabilities.reasoning = true;
    render(
      <ModelMetadataFields
        value={draft}
        disabled={false}
        onChange={vi.fn()}
      />
    );

    expect(screen.getByLabelText("Reasoning efforts")).toBeTruthy();
    expect(screen.getByLabelText("Default reasoning effort")).toBeTruthy();
  });

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
    draft.capabilities.tool_calls = false;
    expect(modelMetadataProblem(draft, "openai_chat")).toBe(
      "QCode requires tool calling."
    );
    draft.capabilities.tool_calls = true;
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
