const politePromptPrefix =
  /^(?:(?:请(?:帮我)?|帮我|麻烦(?:帮我)?|please|can you|could you)\s*)+/iu;
const maxGeneratedChatTitleLength = 48;
const placeholderChatTitle = /^Chat [1-9][0-9]*$/u;

export function isPlaceholderChatTitle(title: string): boolean {
  return placeholderChatTitle.test(title);
}

export function chatTitleFromPrompt(prompt: string): string | undefined {
  let title = prompt
    .replace(/```[\s\S]*?```/gu, " 代码 ")
    .replace(/`([^`\r\n]+)`/gu, "$1")
    .replace(/^[\s#>*\-–—\d.)、]+/u, "")
    .replace(/\s+/gu, " ")
    .trim()
    .replace(politePromptPrefix, "")
    .trim();
  if (title.length === 0) return undefined;
  const sentenceEnd = title.search(/[。！？!?；;]/u);
  if (sentenceEnd >= 6) {
    title = title.slice(0, sentenceEnd);
  }
  const characters = Array.from(title);
  if (characters.length > maxGeneratedChatTitleLength) {
    title = `${characters.slice(0, maxGeneratedChatTitleLength - 1).join("")}…`;
  }
  return title;
}
