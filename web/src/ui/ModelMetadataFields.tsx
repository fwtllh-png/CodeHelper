import type {
  SetupModelCapabilities,
  SetupModelMetadata
} from "../protocol";
import "./SettingsDialog.css";

export interface ModelMetadataDraft {
  canonicalID: string;
  wireID: string;
  contextTokens: string;
  maxOutputTokens: string;
  capabilities: SetupModelCapabilities;
  reasoningEfforts: string;
  defaultReasoningEffort: string;
}

export function emptyModelMetadataDraft(modelID = ""): ModelMetadataDraft {
  return {
    canonicalID: modelID,
    wireID: modelID,
    contextTokens: "",
    maxOutputTokens: "",
    capabilities: {
      streaming: true,
      reasoning: false,
      tool_calls: false,
      native_search: false,
      incremental_responses: false,
      vision: false,
      image_input: false,
      prompt_cache: false,
      automatic_prompt_cache: false,
      thinking_toggle: false
    },
    reasoningEfforts: "",
    defaultReasoningEffort: ""
  };
}

export function modelMetadataDraft(
  metadata?: SetupModelMetadata,
  modelID = ""
): ModelMetadataDraft {
  if (!metadata) return emptyModelMetadataDraft(modelID);
  return {
    canonicalID: metadata.canonical_id,
    wireID: metadata.wire_id,
    contextTokens: String(metadata.context_tokens),
    maxOutputTokens: String(metadata.max_output_tokens),
    capabilities: {...metadata.capabilities, streaming: true},
    reasoningEfforts: metadata.capabilities.reasoning_efforts?.join(", ") ?? "",
    defaultReasoningEffort:
      metadata.capabilities.default_reasoning_effort ?? ""
  };
}

export function modelMetadataForModel(
  draft: ModelMetadataDraft,
  previousModelID: string,
  modelID: string
): ModelMetadataDraft {
  const previous = previousModelID.trim();
  const next = modelID.trim();
  return {
    ...draft,
    canonicalID:
      !draft.canonicalID.trim() || draft.canonicalID.trim() === previous
        ? next
        : draft.canonicalID,
    wireID:
      !draft.wireID.trim() || draft.wireID.trim() === previous
        ? next
        : draft.wireID,
    capabilities: {...draft.capabilities, streaming: true}
  };
}

function efforts(value: string): string[] {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

const modelIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$/;
const validEfforts = "off minimal low medium high xhigh max".split(" ");

export function modelMetadataProblem(
  draft: ModelMetadataDraft,
  protocol: string
): string {
  const contextTokens = Number(draft.contextTokens);
  const maxOutputTokens = Number(draft.maxOutputTokens);
  const capabilities = draft.capabilities;
  const declaredEfforts = efforts(draft.reasoningEfforts);
  const defaultEffort = draft.defaultReasoningEffort.trim();
  if (!modelIDPattern.test(draft.canonicalID.trim()) ||
      !modelIDPattern.test(draft.wireID.trim())) {
    return "Model IDs are invalid.";
  }
  if (!Number.isSafeInteger(contextTokens) || contextTokens <= 0 ||
      !Number.isSafeInteger(maxOutputTokens) || maxOutputTokens <= 0) {
    return "Enter token limits.";
  }
  if (maxOutputTokens > contextTokens) {
    return "Output exceeds context.";
  }
  if (!capabilities.reasoning &&
      (declaredEfforts.length > 0 || defaultEffort ||
       capabilities.thinking_toggle) ||
      declaredEfforts.some((effort) => !validEfforts.includes(effort)) ||
      new Set(declaredEfforts).size !== declaredEfforts.length ||
      Boolean(defaultEffort && !declaredEfforts.includes(defaultEffort))) {
    return "Reasoning settings are invalid.";
  }
  if (capabilities.automatic_prompt_cache && !capabilities.prompt_cache) {
    return "Enable prompt cache first.";
  }
  if (capabilities.incremental_responses &&
      protocol !== "openai_responses") {
    return "Incremental responses require Responses.";
  }
  return "";
}

export function setupModelMetadata(
  draft: ModelMetadataDraft
): SetupModelMetadata {
  return {
    canonical_id: draft.canonicalID.trim(),
    wire_id: draft.wireID.trim(),
    context_tokens: Number(draft.contextTokens),
    max_output_tokens: Number(draft.maxOutputTokens),
    capabilities: {
      ...draft.capabilities,
      reasoning_efforts: draft.capabilities.reasoning
        ? efforts(draft.reasoningEfforts)
        : undefined,
      default_reasoning_effort: draft.capabilities.reasoning
        ? draft.defaultReasoningEffort.trim() || undefined
        : undefined
    }
  };
}

type BooleanCapabilityKey = Exclude<
  keyof SetupModelCapabilities,
  "reasoning_efforts" | "default_reasoning_effort"
>;

const capabilityFields: Array<{
  key: BooleanCapabilityKey;
  label: string;
}> = [
  {key: "tool_calls", label: "Tool calls"},
  {key: "reasoning", label: "Reasoning"},
  {key: "native_search", label: "Native search"},
  {key: "vision", label: "Vision"},
  {key: "image_input", label: "Image input"},
  {key: "prompt_cache", label: "Prompt cache"},
  {key: "automatic_prompt_cache", label: "Automatic prompt cache"},
  {key: "incremental_responses", label: "Incremental responses"},
  {key: "thinking_toggle", label: "Thinking toggle"}
];

type TextFieldKey =
  | "canonicalID"
  | "wireID"
  | "contextTokens"
  | "maxOutputTokens"
  | "reasoningEfforts"
  | "defaultReasoningEffort";

interface TextField {
  key: TextFieldKey;
  label: string;
  type?: "number";
  placeholder?: string;
}

const limitFields: TextField[] = [
  {key: "contextTokens", label: "Context tokens", type: "number"},
  {key: "maxOutputTokens", label: "Max output tokens", type: "number"}
];

const identityFields: TextField[] = [
  {key: "canonicalID", label: "Canonical model ID"},
  {key: "wireID", label: "Wire model ID"}
];

const reasoningFields: TextField[] = [
  {
    key: "reasoningEfforts",
    label: "Reasoning efforts",
    placeholder: "off, low, high"
  },
  {key: "defaultReasoningEffort", label: "Default reasoning effort"}
];

export function ModelMetadataFields({
  value,
  disabled,
  onChange
}: {
  value: ModelMetadataDraft;
  disabled: boolean;
  onChange: (value: ModelMetadataDraft) => void;
}) {
  const setCapability = (
    key: BooleanCapabilityKey,
    checked: boolean
  ) => {
    onChange({
      ...value,
      capabilities: {...value.capabilities, [key]: checked}
    });
  };
  const updateText = (key: TextFieldKey, text: string) => {
    onChange({...value, [key]: text});
  };
  return (
    <>
      <div className="settingsFacts">
        {limitFields.map(({key, label, type}) => (
          <label className="selectField" key={key}>
            <span>{label}</span>
            <input
              className="settingsSelect"
              type={type}
              min="1"
              step="1"
              aria-label={label}
              value={value[key]}
              disabled={disabled}
              onChange={(event) => updateText(key, event.target.value)}
            />
          </label>
        ))}
      </div>
      <details className="settingsCatalogDetails">
        <summary>Advanced model configuration</summary>
        <div className="startupFields" data-custom>
          {identityFields.map(({key, label}) => (
            <label className="selectField" key={key}>
              <span>{label}</span>
              <input
                className="settingsSelect"
                aria-label={label}
                value={value[key]}
                disabled={disabled}
                onChange={(event) => updateText(key, event.target.value)}
              />
            </label>
          ))}
          {capabilityFields.map(({key, label}) => (
            <label className="settingsPreferenceControl" key={key}>
              <input
                type="checkbox"
                aria-label={label}
                checked={Boolean(value.capabilities[key])}
                disabled={disabled}
                onChange={(event) => setCapability(key, event.target.checked)}
              />
              <span>{label}</span>
            </label>
          ))}
          {value.capabilities.reasoning && (
            <>
              {reasoningFields.map(({key, label, placeholder}) => (
                <label className="selectField" key={key}>
                  <span>{label}</span>
                  <input
                    className="settingsSelect"
                    aria-label={label}
                    placeholder={placeholder}
                    value={value[key]}
                    disabled={disabled}
                    onChange={(event) => updateText(key, event.target.value)}
                  />
                </label>
              ))}
            </>
          )}
        </div>
      </details>
    </>
  );
}
