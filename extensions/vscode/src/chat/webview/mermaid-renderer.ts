/// <reference lib="dom" />

import mermaid from "mermaid";

declare global {
  interface Window {
    CodeHelperMermaidRender?: (
      id: string,
      source: string,
    ) => Promise<string>;
  }
}

mermaid.initialize({
  startOnLoad: false,
  securityLevel: "strict",
  suppressErrorRendering: true,
  theme: "dark",
  flowchart: { htmlLabels: false },
});

window.CodeHelperMermaidRender = async (id, source) => {
  const rendered = await mermaid.render(id, source);
  return rendered.svg;
};
