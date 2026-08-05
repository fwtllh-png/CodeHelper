export type ContextDirective = "file" | "selection" | "symbol" | "diagnostics";

export interface ParsedPrompt {
  readonly prompt: string;
  readonly directives: ReadonlySet<ContextDirective>;
}

const directivePattern =
  /(^|\s)@(file|selection|symbol|diagnostics)(?=\s|$|[.,;!?])/gu;

export function parseContextDirectives(value: string): ParsedPrompt {
  const directives = new Set<ContextDirective>();
  const prompt = value.replace(
    directivePattern,
    (_match: string, _prefix: string, directive: ContextDirective) => {
      directives.add(directive);
      return "";
    },
  ).trim();
  return { prompt, directives };
}
