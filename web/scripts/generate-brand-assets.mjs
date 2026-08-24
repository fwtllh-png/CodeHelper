import {chromium} from "playwright";
import {readFile} from "node:fs/promises";
import path from "node:path";
import {fileURLToPath} from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = await readFile(path.join(root, "public/favicon.svg"), "utf8");
const browser = await chromium.launch({headless: true});
try {
  for (const size of [192, 512]) {
    const page = await browser.newPage({
      viewport: {width: size, height: size},
      deviceScaleFactor: 1
    });
    await page.setContent(
      `<style>*{box-sizing:border-box}html,body{margin:0;width:100%;height:100%}svg{display:block;width:100%;height:100%}</style>${source}`
    );
    await page.locator("svg").screenshot({
      path: path.join(root, `public/icon-${size}.png`),
      omitBackground: true
    });
    await page.close();
  }
} finally {
  await browser.close();
}
