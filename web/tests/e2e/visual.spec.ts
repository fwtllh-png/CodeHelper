import {expect, test, type Page} from "@playwright/test";
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
test.setTimeout(90_000);

test.beforeEach(async () => {
  dataDir = await mkdtemp(path.join(tmpdir(), "codehelper-web-visual-"));
  workspaceDir = await mkdtemp(path.join(tmpdir(), "codehelper-web-visual-workspace-"));
  await writeFile(
    path.join(workspaceDir, "README.md"),
    "# Visual fixture\n\nA stable baseline for browser goldens.\n"
  );
  await writeFile(
    path.join(workspaceDir, "main.go"),
    "package main\n\nfunc stableVisualFixture() {}\n"
  );
  execFileSync("git", ["init", "-q"], {cwd: workspaceDir});
  execFileSync("git", ["add", "."], {cwd: workspaceDir});
  execFileSync(
    "git",
    [
      "-c", "core.hooksPath=/dev/null",
      "-c", "user.name=CodeHelper",
      "-c", "user.email=fixture@codehelper.invalid",
      "commit", "-qm", "visual baseline"
    ],
    {cwd: workspaceDir}
  );
  server = spawn(
    path.join(repositoryRoot, "bin/codehelper"),
    [
      "web",
      "--workspace", workspaceDir,
      "--data-dir", dataDir,
      "--provider-fixture",
      path.join(repositoryRoot, "testdata/providers/web-visual"),
      "--provider", "openai",
      "--model", "fixture-model",
      "--enable-tools",
      "--posture", "suggest",
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

test.beforeEach(async ({page}) => {
  await page.setViewportSize({width: 1440, height: 900});
  await page.emulateMedia({
    colorScheme: "light",
    forcedColors: "none",
    reducedMotion: "reduce"
  });
  await page.goto(baseURL);
  await expect(page.locator(".app")).toBeVisible();
});

test("captures empty and settings states", async ({page}) => {
  await expect(page).toHaveScreenshot("canonical-empty.png");

  await page.getByRole("button", {name: "Settings"}).click();
  await expect(page.getByRole("dialog", {name: "Settings"})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-settings.png");
});

test("captures populated model, tool, and agent settings", async ({page}) => {
  await createSession(page);
  await page.getByRole("button", {name: "Settings"}).click();
  await page.getByRole("button", {name: "Models"}).click();
  await expect(page.getByText("Context window")).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-settings-models.png");

  await page.getByRole("button", {name: "Tools"}).click();
  await expect(page.getByRole("searchbox", {name: "Search tools"})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-settings-tools.png");

  await page.getByRole("button", {name: "Agent preset"}).click();
  await expect(page.getByLabel("Agent mode")).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-settings-agent.png");
});

test("captures a blank session with the centered composer", async ({page}) => {
  await createSession(page);
  await expect(page.getByLabel("Session details")).toHaveCount(0);
  await expect(page).toHaveScreenshot("canonical-blank-session.png");
});

test("captures the modal workspace context browser", async ({page}) => {
  await createSession(page);
  await page.getByRole("button", {name: "Commands"}).click();
  await page.getByRole("menuitem", {name: /context/}).click();
  await expect(page.getByRole("dialog", {name: "Add context"})).toBeVisible();
  await expect(page.getByRole("button", {name: /README.md/})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-context-browser.png");
});

test("captures the authoritative diff state", async ({page}) => {
  await createSession(page);
  await page.getByLabel("Approval").selectOption("auto");
  await submitPrompt(page, "visual diff");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Updated README and verified the diff.");
  await expect(page.getByText("Completed", {exact: true})).toHaveCount(0);
  const edit = page.locator('.toolDisclosure[data-variant="diff"]');
  await edit.locator(".disclosureLeading").click();
  await expect(edit.locator("[data-diff]")).toContainText("README.md");
  await expect(edit.locator(".diffFooter")).toContainText("+2 -1");
  await expect(page).toHaveScreenshot("canonical-diff.png");
});

test("captures collapsed tools, expanded tool detail, and trajectory", async ({page}) => {
  await createSession(page);
  await page.getByLabel("Approval").selectOption("auto");
  await submitPrompt(page, "visual diff");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Updated README and verified the diff.");
  await expect(page.getByLabel("Session details")).toHaveCount(0);

  const tool = page.locator(".toolDisclosure").first();
  await expect(tool).toHaveAttribute("data-call-id", /.+/);
  await expect(tool.locator("pre")).toHaveCount(0);
  await expect(tool.locator(".disclosureChevron")).toHaveCSS("opacity", "0");
  await expect(tool.locator(":scope > .disclosureRow")).not.toContainText("completed");
  await expect(page).toHaveScreenshot("canonical-tool-collapsed.png");

  await tool.locator(":scope > .disclosureRow").hover();
  await expect(tool.locator(".disclosureChevron")).toHaveCSS("opacity", "1");
  await tool.locator(":scope > .disclosureRow .disclosureLeading").click();
  await expect(tool.locator(
    ".toolIOCard, [data-read], [data-terminal], [data-search], [data-diff]"
  )).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-tool-expanded.png");

  await tool.getByRole("button", {name: "Inspect"}).click();
  await expect(page.getByLabel("Execution trajectory")).toBeVisible();
  const inspector = page.getByRole("complementary", {name: "Record inspector"});
  await expect(inspector).toBeVisible();
  await expect(inspector.getByRole("tab", {name: "Summary"}))
    .toHaveAttribute("aria-selected", "true");
  await expect(inspector.getByRole("tab", {name: "Input"})).toBeVisible();
  await expect(inspector.getByRole("tab", {name: "Output"})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-trajectory.png");
  await inspector.getByRole("tab", {name: "Output"}).click();
  await expect(inspector.getByRole("button", {name: "Copy output"})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-trajectory-detail.png");
});

test("captures Think and specialized Read, Bash, Grep, and Glob cards", async ({page}) => {
  await createSession(page);
  await page.getByLabel("Approval").selectOption("auto");
  await submitPrompt(page, "visual tools");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Inspected the workspace with focused tools.");

  const think = page.locator(".reasoningDisclosure");
  await expect(think).toContainText("Think");
  await expect(think).toContainText("I will inspect the file");

  const read = page.locator('.toolDisclosure[data-variant="read"]').first();
  await read.locator(".disclosureLeading").click();
  await expect(read.locator("[data-read]")).toBeVisible();
  await read.scrollIntoViewIfNeeded();
  await expect(page).toHaveScreenshot("canonical-think-read.png");

  const bash = page.locator('.toolDisclosure[data-variant="shell"]').first();
  await bash.locator(".disclosureRow").click();
  await expect(bash.locator("[data-terminal]")).toBeVisible();
  await bash.scrollIntoViewIfNeeded();
  await expect(page).toHaveScreenshot("canonical-bash.png");

  const searches = page.locator('.toolDisclosure[data-variant="search"]');
  await searches.nth(0).locator(".disclosureRow").click();
  await searches.nth(1).locator(".disclosureRow").click();
  await expect(searches.nth(0).locator("[data-search]")).toBeVisible();
  await expect(searches.nth(1).locator("[data-search]")).toBeVisible();
  await searches.nth(0).scrollIntoViewIfNeeded();
  await expect(page).toHaveScreenshot("canonical-search.png");
});

test("captures the back-to-bottom control at the transcript edge", async ({page}) => {
  await page.setViewportSize({width: 1024, height: 600});
  await createSession(page);
  await page.getByLabel("Approval").selectOption("auto");
  await submitPrompt(page, "visual tools");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Inspected the workspace with focused tools.");
  for (const row of await page.locator(".toolDisclosure .disclosureRow").all()) {
    await row.click();
  }
  await page.locator(".conversationScrollport").evaluate((element) => {
    element.scrollTop = 0;
    element.dispatchEvent(new Event("scroll"));
  });
  const button = page.getByRole("button", {name: "Back to bottom"});
  await expect(button).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-back-to-bottom.png");
  await button.click();
  await expect(button).toHaveCount(0);
});

test("captures streaming and completed states", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual streaming");
  await expect(page.getByText("Working", {exact: true})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-streaming.png");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Review complete. Runtime evidence is consistent.");
  await expect(page.getByText("Completed", {exact: true})).toHaveCount(0);
  await expect(page).toHaveScreenshot("canonical-completed.png");
});

test("captures message actions, commands, context usage, and rich Markdown", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual chrome");
  await expect(page.getByRole("heading", {name: "Core modules"})).toBeVisible();
  await expect(page.getByRole("table")).toBeVisible();
  await expect(page.getByRole("button", {name: "Copy response"})).toBeVisible();
  await expect(page.getByRole("button", {
    name: "Like response",
    exact: true
  })).toBeVisible();
  await expect(page.getByRole("button", {
    name: "Dislike response",
    exact: true
  })).toBeVisible();

  const selectWidths = await page.evaluate(() => ({
    mode: document.querySelector<HTMLSelectElement>('select[aria-label="Mode"]')
      ?.getBoundingClientRect().width ?? 0,
    approval: document.querySelector<HTMLSelectElement>('select[aria-label="Approval"]')
      ?.getBoundingClientRect().width ?? 0
  }));
  expect(selectWidths.mode).toBeLessThan(72);
  expect(selectWidths.approval).toBeLessThan(92);
  await expect(page).toHaveScreenshot("canonical-message-chrome.png");

  await page.getByRole("button", {name: "Commands"}).click();
  await expect(page.getByRole("menu", {name: "Commands"})).toBeVisible();
  await expect(page.getByRole("menuitem", {name: /compact/})).toBeEnabled();
  await expect(page).toHaveScreenshot("canonical-command-menu.png");
  await page.getByRole("button", {name: "Commands"}).click();

  const context = page.getByRole("button", {name: /of context used/});
  await expect(context).toBeVisible();
  await context.click();
  await expect(page.getByRole("dialog", {name: "Context usage"})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-context-usage.png");

  await page.getByRole("button", {
    name: "Dislike response",
    exact: true
  }).click();
  await expect(page.getByRole("button", {name: "Remove dislike"}))
    .toHaveAttribute("aria-pressed", "true");
  await page.waitForTimeout(200);
  await page.reload();
  await expect(page.getByRole("button", {name: "Remove dislike"}))
    .toHaveAttribute("aria-pressed", "true");
});

test("captures the approval state", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual approval");
  await expect(page.getByText("exec_command requires approval")).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-approval.png");
});

test("captures a complex edit and approval workflow", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual edit approval");

  await expect(page.getByText("Waiting for approval")).toBeVisible();
  await expect(page.getByText("exec_command requires approval")).toBeVisible();
  const pendingEdit = page.locator('.toolDisclosure[data-variant="diff"]');
  await pendingEdit.locator(".disclosureLeading").click();
  await expect(pendingEdit.locator("[data-diff]")).toContainText("README.md");
  await expect(page).toHaveScreenshot("canonical-edit-approval.png");

  await page.getByRole("button", {name: "Approve once"}).click();
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Updated");
  await expect(pendingEdit.locator("[data-diff]")).toContainText("README.md");
  await expect(page).toHaveScreenshot("canonical-edit-completed.png");
});

test("captures the input state", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual input");
  await expect(page.getByText("Choose the verification scope")).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-input.png");
});

test("captures the failure state", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual failure");
  await expect(page.getByText("Failed", {exact: true})).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-failure.png");
});

test("repeated reloads do not resubmit an active streaming turn", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual long streaming");
  await expect(page.getByText("Working", {exact: true})).toBeVisible();

  for (let index = 0; index < 5; index += 1) {
    await page.reload();
    await expect(page.locator(".app")).toBeVisible();
  }

  await expect(page.getByText("Connected", {exact: true})).toBeVisible();
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Review complete. Runtime evidence is consistent.");
  await expect(page.locator(".userMessage", {
    hasText: "visual long streaming"
  })).toHaveCount(1);
});

test("a frozen tab converges after streaming completes", async ({page, context}) => {
  await createSession(page);
  await submitPrompt(page, "visual long streaming");
  await expect(page.getByText("Working", {exact: true})).toBeVisible();

  const session = await context.newCDPSession(page);
  await session.send("Page.setWebLifecycleState", {state: "frozen"});
  await new Promise((resolve) => setTimeout(resolve, 3_000));
  await session.send("Page.setWebLifecycleState", {state: "active"});
  await page.bringToFront();

  await expect(page.locator(".assistantMessage").last())
    .toContainText("Review complete. Runtime evidence is consistent.");
  await expect(page.locator(".userMessage", {
    hasText: "visual long streaming"
  })).toHaveCount(1);
  await session.detach();
});

test("captures viewport theme contrast and zoom matrix", async ({page}) => {
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
      await assertViewportGeometry(page);
      await expect(page).toHaveScreenshot(
        `viewport-${viewport.width}x${viewport.height}-${colorScheme}.png`
      );
    }
  }

  await page.setViewportSize({width: 1024, height: 768});
  await page.emulateMedia({
    colorScheme: "light",
    forcedColors: "active",
    reducedMotion: "reduce"
  });
  await page.goto(baseURL);
  await assertViewportGeometry(page);
  await expect(page).toHaveScreenshot("viewport-1024x768-forced-colors.png");

  await page.emulateMedia({
    colorScheme: "light",
    forcedColors: "none",
    reducedMotion: "reduce"
  });
  await page.setViewportSize({width: 512, height: 384});
  await page.goto(baseURL);
  await assertViewportGeometry(page);
  await page.getByRole("button", {name: "Create session"}).scrollIntoViewIfNeeded();
  await expect(page.getByRole("button", {name: "Create session"})).toBeVisible();
  await expect(page).toHaveScreenshot("viewport-1024x768-zoom-200.png");
});

async function createSession(page: Page): Promise<void> {
  const sessions = page.locator(".sessionRow");
  const count = await sessions.count();
  await page.locator('button[aria-label="New chat"]').click();
  await expect(sessions).toHaveCount(count + 1);
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
}

async function submitPrompt(page: Page, prompt: string): Promise<void> {
  const composer = page.getByPlaceholder("Ask CodeHelper");
  await composer.fill(prompt);
  await page.getByRole("button", {name: "Send"}).click();
}

async function assertViewportGeometry(page: Page): Promise<void> {
  await expect(page.locator(".app")).toBeVisible();
  const geometry = await page.evaluate(() => {
    const app = document.querySelector<HTMLElement>(".app");
    const actions = Array.from(document.querySelectorAll<HTMLElement>("button"))
      .filter((button) => button.offsetParent !== null)
      .map((button) => button.getBoundingClientRect())
      .filter((box) => box.bottom > 0 && box.top < window.innerHeight);
    const primary = document.querySelector<HTMLElement>(
      ".composerSeat, .startupSetup"
    );
    const primaryBox = primary?.getBoundingClientRect();
    return {
      appOverflow: app ? app.scrollWidth - app.clientWidth : -1,
      actionsInside: actions.every(
        (box) =>
          box.left >= 0 &&
          box.right <= window.innerWidth &&
          box.top >= 0 &&
          box.bottom <= window.innerHeight
      ),
      primaryVisible: primaryBox
        ? primaryBox.top < window.innerHeight && primaryBox.bottom > 0
        : false
    };
  });
  expect(geometry.appOverflow).toBeLessThanOrEqual(0);
  expect(geometry.actionsInside).toBe(true);
  expect(geometry.primaryVisible).toBe(true);
}

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
