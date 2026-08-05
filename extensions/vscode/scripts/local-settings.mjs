import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

import { applyEdits, format, modify, parse } from "jsonc-parser";

const [settingsPath, configPath] = process.argv.slice(2);
if (settingsPath === undefined || configPath === undefined) {
  throw new Error("usage: local-settings.mjs SETTINGS_PATH CONFIG_PATH");
}

await mkdir(dirname(settingsPath), { recursive: true });
let source = "{}\n";
try {
  source = await readFile(settingsPath, "utf8");
} catch (error) {
  if (error?.code !== "ENOENT") throw error;
}

const errors = [];
const parsed = parse(source, errors, { allowTrailingComma: true });
if (errors.length > 0 || parsed === null || typeof parsed !== "object" ||
  Array.isArray(parsed)) {
  throw new Error(`VS Code User Settings is not valid JSONC: ${settingsPath}`);
}

const formattingOptions = {
  insertSpaces: true,
  tabSize: 4,
  eol: "\n",
};
for (const [path, value] of [
  [["codehelper.binarySource"], "auto"],
  [["codehelper.runtime.configPath"], configPath],
]) {
  source = applyEdits(source, modify(source, path, value, { formattingOptions }));
}
source = applyEdits(source, format(source, undefined, formattingOptions));
if (!source.endsWith("\n")) source += "\n";
await writeFile(settingsPath, source, { encoding: "utf8", mode: 0o600 });
