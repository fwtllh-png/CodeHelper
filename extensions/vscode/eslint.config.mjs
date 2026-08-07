import eslint from "@eslint/js";
import { defineConfig } from "eslint/config";
import tseslint from "typescript-eslint";

export default defineConfig(
  {
    ignores: [
      "dist/**",
      ".tmp-tests/**",
      ".tmp-electron/**",
      ".vscode-test/**",
      "node_modules/**",
    ],
  },
  eslint.configs.recommended,
  {
    files: ["src/**/*.ts"],
    extends: [...tseslint.configs.strictTypeChecked],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      "@typescript-eslint/consistent-type-imports": "error",
      "@typescript-eslint/no-confusing-void-expression": "error",
      "@typescript-eslint/no-import-type-side-effects": "error"
    },
  },
  {
    files: ["src/chat/webview/client.ts"],
    languageOptions: {
      parserOptions: {
        project: "./tsconfig.webview.json",
        projectService: false,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ["scripts/**/*.mjs"],
    languageOptions: {
      globals: {
        console: "readonly",
        process: "readonly"
      }
    }
  },
  {
    files: ["eslint.config.mjs"],
    languageOptions: {
      globals: {
        process: "readonly"
      }
    }
  }
);
