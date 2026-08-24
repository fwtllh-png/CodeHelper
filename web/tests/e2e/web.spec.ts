import {expect, test} from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import {
  execFileSync,
  spawn,
  type ChildProcessWithoutNullStreams
} from "node:child_process";
import {mkdtemp, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import {fileURLToPath} from "node:url";

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../.."
);

let server: ChildProcessWithoutNullStreams;
let dataDir: string;
let workspaceDir: string;
let baseURL: string;

test.describe.configure({mode: "serial"});

test.beforeEach(async () => {
  dataDir = await mkdtemp(path.join(tmpdir(), "codehelper-web-e2e-"));
  workspaceDir = await mkdtemp(path.join(tmpdir(), "codehelper-web-workspace-"));
  await writeFile(
    path.join(workspaceDir, "README.md"),
    "# Fixture workspace\n\nhello from the browser test\n"
  );
  await writeFile(
    path.join(workspaceDir, "main.go"),
    "package main\n\nfunc helloFixture() {}\n"
  );
  await writeFile(
    path.join(workspaceDir, "diagram.png"),
    Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
      "base64"
    )
  );
  execFileSync("git", ["init", "-q"], {cwd: workspaceDir});
  server = spawn(
    path.join(repositoryRoot, "bin/codehelper"),
    [
      "web",
      "--workspace", workspaceDir,
      "--data-dir", dataDir,
      "--provider-fixture", path.join(repositoryRoot, "testdata/providers/openai"),
      "--provider", "openai",
      "--model", "fixture-model",
      "--port", "0",
      "--no-open"
    ],
    {
      cwd: repositoryRoot,
      stdio: ["ignore", "pipe", "pipe"]
    }
  );
  baseURL = await runtimeURL(server);
});

test.afterEach(async () => {
  if (server && server.exitCode === null) {
    server.kill("SIGINT");
    await Promise.race([
      new Promise<void>((resolve) => server.once("exit", () => resolve())),
      new Promise<void>((resolve) => setTimeout(resolve, 10_000))
    ]);
    if (server.exitCode === null) server.kill("SIGKILL");
  }
  if (dataDir) await rm(dataDir, {recursive: true, force: true});
  if (workspaceDir) await rm(workspaceDir, {recursive: true, force: true});
});

test("boots the real Runtime with an accessible empty state", async ({page}) => {
  await page.goto(baseURL);

  await expect(page.getByRole("heading", {name: "New Chat"})).toBeVisible();
  await expect(page.getByText("Connected", {exact: true})).toBeVisible();
  await expect(page.locator('button[aria-label="New chat"]')).toBeVisible();
  await expect(page.getByRole("button", {name: "Settings"})).toBeVisible();
  await expect(page.getByRole("textbox", {name: "Search sessions"})).toBeVisible();
  await expect(page.getByRole("heading", {name: "Start a new session"})).toBeVisible();
  await expect(page.getByText("Not required", {exact: true})).toBeVisible();
  await expect(page.getByRole("button", {name: "Create session"})).toBeVisible();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toHaveCount(0);
  await expect(page.getByLabel("Session details")).toHaveCount(0);
  await expect(page.getByRole("button", {name: /detail panel/i})).toHaveCount(0);

  await page.locator('button[aria-label="New chat"]').focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("textbox", {name: "Search sessions"})).toBeFocused();
});

test("passes the WCAG A and AA accessibility scan", async ({page}) => {
  for (const colorScheme of ["light", "dark"] as const) {
    await page.emulateMedia({colorScheme});
    await page.goto(baseURL);

    for (const state of ["empty", "settings"] as const) {
      if (state === "settings") {
        await page.getByRole("button", {name: "Settings"}).click();
      }
      const results = await new AxeBuilder({page})
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(results.violations, `${colorScheme} ${state}`).toEqual([]);
    }
  }
});

test("creates a Session and completes a fixture-backed Turn", async ({page}) => {
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();

  const composer = page.getByPlaceholder("Ask CodeHelper");
  await expect(composer).toBeEnabled();
  await expect(page.getByLabel("Session details")).toHaveCount(0);
  await composer.fill("say hello");
  await page.getByRole("button", {name: "Send"}).click();

  await expect(page.getByText("hello", {exact: true}).last()).toBeVisible();
  await expect(page.getByText("Working", {exact: true})).toHaveCount(0);
});

test("opens the execution trajectory and inspects its event ledger", async ({page}) => {
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();
  await page.getByPlaceholder("Ask CodeHelper").fill("say hello");
  await page.getByRole("button", {name: "Send"}).click();
  await expect(page.getByText("hello", {exact: true}).last()).toBeVisible();

  await page.getByRole("button", {name: "Trajectory"}).click();
  const trajectory = page.getByLabel("Execution trajectory");
  await expect(trajectory).toBeVisible();
  await expect(trajectory.locator(".timelineLabels")).toHaveText("InputModelTools");
  await expect(trajectory.locator(".ledgerRow")).not.toHaveCount(0);

  await trajectory.locator(".ledgerRow").first().click();
  await expect(page.getByRole("complementary", {name: "Record inspector"}))
    .toBeVisible();
  await expect(page.getByRole("button", {name: "Previous record"})).toBeDisabled();
});

test("deletes the final Session after explicit confirmation", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await expect(page.locator(".sessionRow")).toHaveCount(1);
  await page.getByRole("button", {name: "Add context"}).click();
  page.once("dialog", (dialog) => dialog.accept());

  await page.locator(".detailPanel").getByRole("button", {
    name: "Delete session"
  }).click();

  await expect(page.locator(".sessionRow")).toHaveCount(0);
  await expect(page.getByRole("heading", {name: "Start a new session"})).toBeVisible();
  await expect(page.getByRole("button", {name: "Create session"})).toBeVisible();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toHaveCount(0);
  await expect(page.getByLabel("Session details")).toHaveCount(0);
});

test("restores the selected Session and transcript after a browser reload", async ({page}) => {
  await page.goto(baseURL);
  const sessionRows = page.locator(".sessionRow");
  const sessionCount = await sessionRows.count();
  await page.locator('button[aria-label="New chat"]').click();
  await expect(sessionRows).toHaveCount(sessionCount + 1);

  const composer = page.getByPlaceholder("Ask CodeHelper");
  await composer.fill("say hello");
  await page.getByRole("button", {name: "Send"}).click();
  await expect(page.getByText("hello", {exact: true}).last()).toBeVisible();

  await page.reload();

  await expect(page.getByText("Connected", {exact: true})).toBeVisible();
  await expect(page.getByText("hello", {exact: true}).last()).toBeVisible();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
});

test("shows the fixed provider and single-model route as read-only", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await page.getByRole("button", {name: "Add context"}).click();

  const provider = page.getByLabel("Provider");
  const model = page.getByLabel("Model");
  await expect(provider).toHaveText("fixture");
  await expect(model).toHaveText("fixture-model");
  await expect(provider).toHaveJSProperty("tagName", "OUTPUT");
  await expect(model).toHaveJSProperty("tagName", "OUTPUT");
});

test("browses workspace resources and restores an archived Session", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
  await page.getByRole("button", {name: "Add context"}).click();

  await page.getByRole("button", {name: "Browse workspace"}).click();
  const fileEntry = page.locator(".workspaceEntries .resourceMatch").filter({
    has: page.getByText("README.md", {exact: true})
  });
  await expect(fileEntry).toBeVisible();
  await fileEntry.click();
  await expect(page.locator(".resourceViewer")).toBeVisible();
  const resourceContent = page.getByLabel("Workspace resource content");
  await resourceContent.focus();
  await resourceContent.evaluate((element: HTMLTextAreaElement) => {
    element.setSelectionRange(0, Math.min(5, element.value.length));
    element.dispatchEvent(new Event("select", {bubbles: true}));
    document.dispatchEvent(new Event("selectionchange", {bubbles: true}));
  });
  await page.getByRole("button", {name: "Add selection to prompt context"}).click();
  await expect(page.getByLabel("Prompt context", {exact: true})).toContainText(
    /:1:1-1:6/
  );
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", {name: "Download resource"}).click()
  ]);
  expect(download.suggestedFilename()).toBe(
    await fileEntry.locator("strong").textContent()
  );

  const symbolSearch = page.getByLabel("Search workspace symbols");
  await symbolSearch.fill("helloFixture");
  await symbolSearch.press("Enter");
  const symbol = page.getByRole("button", {name: /helloFixture.*function.*main.go:3/});
  await expect(symbol).toBeVisible();
  await symbol.click();
  await expect(page.getByLabel("Prompt context", {exact: true})).toContainText("main.go");

  const imageEntry = page.locator(".workspaceEntries .resourceMatch").filter({
    has: page.getByText("diagram.png", {exact: true})
  });
  await imageEntry.click();
  await expect(page.getByRole("img", {name: "diagram.png"})).toBeVisible();
  await page.getByRole("button", {name: "Add image to prompt context"}).click();
  await expect(page.getByLabel("Prompt context", {exact: true})).toContainText(
    "diagram.png"
  );

  const lifecycle = page.locator(".detailSection").filter({
    has: page.getByRole("heading", {name: "Lifecycle"})
  });
  page.once("dialog", (dialog) => dialog.accept("Archive Target"));
  await lifecycle.getByRole("button", {name: "Rename session"}).click();
  await expect(page.locator(".sessionRow").filter({
    hasText: "Archive Target"
  })).toBeVisible();

  page.once("dialog", (dialog) => dialog.accept());
  await lifecycle.getByRole("button", {name: "Archive session"}).click();
  await expect(page.getByRole("heading", {name: "Archive Target", level: 1})).toHaveCount(0);

  await page.getByRole("button", {name: "Show archived sessions"}).click();
  const archived = page.locator(".sessionRow").filter({
    has: page.getByText("Archive Target", {exact: true})
  });
  await archived.locator(".sessionSelect").click();
  await expect(lifecycle.getByRole("button", {name: "Restore session"})).toBeVisible();
  await lifecycle.getByRole("button", {name: "Restore session"}).click();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
});

test("keeps primary UI inside supported viewports with reduced motion", async ({page}) => {
  for (const viewport of [
    {width: 390, height: 844},
    {width: 1024, height: 768},
    {width: 1440, height: 900},
    {width: 1920, height: 1080}
  ]) {
    for (const colorScheme of ["light", "dark"] as const) {
      await page.setViewportSize(viewport);
      await page.emulateMedia({
        colorScheme,
        forcedColors: "none",
        reducedMotion: "reduce"
      });
      await page.goto(baseURL);
      await expect(page.locator(".app")).toBeVisible();

      const geometry = await page.evaluate(() => {
        const app = document.querySelector<HTMLElement>(".app");
        const buttons = Array.from(document.querySelectorAll<HTMLElement>("button"))
          .filter((button) => button.offsetParent !== null)
          .map((button) => button.getBoundingClientRect());
        return {
          appOverflow: app ? app.scrollWidth - app.clientWidth : -1,
          buttonsInside: buttons.every(
            (box) => box.left >= 0 && box.right <= window.innerWidth
          )
        };
      });

      expect(geometry.appOverflow).toBeLessThanOrEqual(0);
      expect(geometry.buttonsInside).toBe(true);
    }
  }

  await page.setViewportSize({width: 1024, height: 768});
  await page.emulateMedia({forcedColors: "active"});
  await page.goto(baseURL);
  await expect(page.locator('button[aria-label="New chat"]')).toBeVisible();
  await expect(page.getByRole("button", {name: "Settings"})).toBeVisible();

  await page.emulateMedia({forcedColors: "none"});
  await page.setViewportSize({width: 512, height: 384});
  const zoomed = await page.evaluate(() => ({
    overflow: document.documentElement.scrollWidth - window.innerWidth,
    composerVisible: Boolean(
      document.querySelector<HTMLElement>(".composer, .startupSetup")?.offsetParent
    )
  }));
  expect(zoomed.overflow).toBeLessThanOrEqual(0);
  expect(zoomed.composerVisible).toBe(true);

  await page.emulateMedia({forcedColors: "none", reducedMotion: "no-preference"});
  await page.evaluate(() => {
    const spinner = document.createElement("span");
    spinner.className = "spin";
    spinner.dataset.testid = "motion-probe";
    document.body.append(spinner);
  });
  await expect.poll(() => page.locator('[data-testid="motion-probe"]').evaluate(
    (element) => element.getAnimations()[0]?.effect?.getTiming().iterations
  )).toBe(Infinity);

  await page.emulateMedia({reducedMotion: "reduce"});
  await expect.poll(() => page.locator('[data-testid="motion-probe"]').evaluate(
    (element) => element.getAnimations()[0]?.effect?.getTiming().iterations ?? 0
  )).toBeLessThanOrEqual(1);
});

function runtimeURL(child: ChildProcessWithoutNullStreams): Promise<string> {
  return new Promise((resolve, reject) => {
    let stdout = "";
    let stderr = "";
    let settled = false;
    const finish = (error?: Error, url?: string) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      child.stdout.off("data", onStdout);
      child.stderr.off("data", onStderr);
      child.off("exit", onExit);
      child.off("error", onError);
      if (error) reject(error);
      else resolve(url!);
    };
    const onStdout = (chunk: Buffer) => {
      stdout += chunk.toString();
      const match = stdout.match(/CodeHelper Runtime Ready: (http:\/\/[^\s]+)/);
      if (match) finish(undefined, match[1]);
    };
    const onStderr = (chunk: Buffer) => {
      stderr += chunk.toString();
    };
    const onExit = (code: number | null) => {
      finish(new Error(`Runtime exited before readiness (${code})\n${stderr}`));
    };
    const onError = (error: Error) => finish(error);
    const timeout = setTimeout(() => {
      finish(new Error(`Runtime readiness timed out\nstdout:\n${stdout}\nstderr:\n${stderr}`));
    }, 20_000);
    child.stdout.on("data", onStdout);
    child.stderr.on("data", onStderr);
    child.once("exit", onExit);
    child.once("error", onError);
  });
}
