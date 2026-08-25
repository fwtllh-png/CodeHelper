import {CapybaraMark} from "./CapybaraMark";

export function CodeHelperWordmark({hero = false}: {hero?: boolean}) {
  return (
    <span className="codeHelperWordmark" data-hero={hero || undefined}>
      <CapybaraMark size={hero ? "hero" : "compact"} />
      <span>CodeHelper</span>
    </span>
  );
}
