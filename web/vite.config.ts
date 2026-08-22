import react from "@vitejs/plugin-react";
import {readdirSync, readFileSync, statSync, writeFileSync} from "node:fs";
import {join, resolve} from "node:path";
import {
  brotliCompressSync,
  constants,
  gzipSync
} from "node:zlib";
import {defineConfig, type Plugin} from "vite";

export default defineConfig({
  plugins: [react(), precompress()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    assetsDir: "assets"
  },
  server: {
    host: "127.0.0.1",
    port: 4173
  }
});

function precompress(): Plugin {
  let outputRoot = "";
  return {
    name: "codehelper-precompress",
    apply: "build",
    configResolved(config) {
      outputRoot = resolve(config.root, config.build.outDir);
    },
    closeBundle() {
      for (const path of filesUnder(outputRoot)) {
        if (!path.endsWith(".js") && !path.endsWith(".css")) continue;
        const content = readFileSync(path);
        writeFileSync(`${path}.gz`, gzipSync(content, {level: 9}));
        writeFileSync(`${path}.br`, brotliCompressSync(content, {
          params: {[constants.BROTLI_PARAM_QUALITY]: 11}
        }));
      }
    }
  };
}

function filesUnder(root: string): string[] {
  return readdirSync(root).flatMap((name) => {
    const path = join(root, name);
    return statSync(path).isDirectory() ? filesUnder(path) : [path];
  });
}
