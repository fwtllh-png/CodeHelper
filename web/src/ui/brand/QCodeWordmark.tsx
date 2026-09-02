import {CapybaraMark} from "./CapybaraMark";

export function QCodeWordmark({hero = false}: {hero?: boolean}) {
  return (
    <span className="qCodeWordmark" data-hero={hero || undefined}>
      <CapybaraMark size={hero ? "hero" : "compact"} />
      <span>QCode</span>
    </span>
  );
}
