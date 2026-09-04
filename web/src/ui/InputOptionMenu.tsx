import {Check} from "lucide-react";

interface Props {
  value: string;
  options: readonly string[];
  disabled?: boolean;
  onChange: (value: string) => void;
}

// Input options are model-provided suggestions, not a compact settings menu.
// Keep the choices visible so the user can compare them before deciding.
export function InputOptionMenu({
  value,
  options,
  disabled,
  onChange
}: Props) {
  return (
    <section className="inputOptionRoot" aria-label="Suggested answers">
      <div className="inputOptionHeading">
        <span>Suggested answers</span>
        <small>Select one or write your own</small>
      </div>
      <div className="inputOptionList" role="group" aria-label="Suggested answers">
        {options.map((option, index) => {
          const selected = option === value;
          return (
            <button
              className="inputOptionCard"
              key={option}
              type="button"
              aria-pressed={selected}
              disabled={disabled}
              data-selected={selected || undefined}
              onClick={() => onChange(option)}
            >
              <span className="inputOptionText">
                <span className="inputOptionIndex" aria-hidden="true">
                  {index + 1}
                </span>
                <span>{option}</span>
              </span>
              {selected && <Check className="inputOptionCheck" size={16} aria-hidden="true" />}
            </button>
          );
        })}
      </div>
    </section>
  );
}
