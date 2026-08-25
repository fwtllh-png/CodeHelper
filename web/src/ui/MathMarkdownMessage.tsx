import ReactMarkdown, {type Components} from "react-markdown";
import rehypeKatex from "rehype-katex";
import remarkCjkFriendly from "remark-cjk-friendly/parseOnly";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import {safeMarkdownURL} from "./markdownURL";

export function MathMarkdownMessage({
  text,
  components
}: {
  text: string;
  components: Components;
}) {
  return (
    <div className="assistantMarkdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkCjkFriendly, remarkMath]}
        rehypePlugins={[[rehypeKatex, {
          output: "mathml",
          strict: "ignore",
          throwOnError: false
        }]]}
        components={components}
        urlTransform={safeMarkdownURL}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}
