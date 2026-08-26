import {expect, test, type Page} from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import {
  execFileSync,
  spawn,
  type ChildProcessByStdio
} from "node:child_process";
import {mkdtemp, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import type {Readable} from "node:stream";
import {fileURLToPath} from "node:url";

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../.."
);

let server: ChildProcessByStdio<null, Readable, Readable>;
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
      "--workspace", workspaceDir,
      "--data-dir", dataDir,
      "--provider-fixture", path.join(repositoryRoot, "testdata/providers/openai"),
      "--provider", "openai",
      "--model", "fixture-model",
      "--enable-tools=false",
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
  const searchSessions = page.getByRole("button", {name: "Search sessions"});
  await expect(searchSessions).toBeVisible();
  await searchSessions.click();
  await expect(page.getByRole("textbox", {name: "Search sessions"})).toBeFocused();
  await expect(page.getByRole("heading", {name: "Start a new session"})).toBeVisible();
  await expect(page.getByRole("button", {name: "Create session"})).toBeVisible();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toHaveCount(0);
  await expect(page.getByLabel("Session details")).toHaveCount(0);
  await expect(page.getByRole("button", {name: /detail panel/i})).toHaveCount(0);

});

test("requires Workspace selection on the bare Supervisor URL", async ({page}) => {
  await page.goto(new URL("/", baseURL).toString());

  await expect(page.getByRole("heading", {name: "Choose a workspace"}))
    .toBeVisible();
  await expect(page.getByRole("button", {name: "Select workspace"}))
    .toBeVisible();
  await expect(page.locator('button[aria-label="New chat"]')).toHaveCount(0);
  await page.getByRole("button", {name: "Choose workspace"}).click();
  await expect(page.getByRole("dialog", {name: "Workspaces"})).toBeVisible();
});

test("changes reasoning effort from an upward composer menu", async ({page}) => {
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();

  const trigger = page.getByRole("button", {name: "Reasoning"});
  await expect(trigger).toContainText("Default");
  await trigger.click();

  const menu = page.getByRole("menu", {name: "Reasoning modes"});
  await expect(menu).toBeVisible();
  expect(await menu.getByRole("menuitemradio").allTextContents()).toEqual([
    "Default",
    "Low",
    "Medium",
    "High",
    "XHigh"
  ]);
  const desktopBounds = await menu.boundingBox();
  expect(desktopBounds).not.toBeNull();
  expect(desktopBounds!.y).toBeGreaterThanOrEqual(0);

  await menu.getByRole("menuitemradio", {name: "High", exact: true}).click();
  await expect(trigger).toContainText("High");

  await page.setViewportSize({width: 390, height: 844});
  await trigger.click();
  const mobileBounds = await menu.boundingBox();
  expect(mobileBounds).not.toBeNull();
  expect(mobileBounds!.x).toBeGreaterThanOrEqual(0);
  expect(mobileBounds!.x + mobileBounds!.width).toBeLessThanOrEqual(390);
  expect(mobileBounds!.y).toBeGreaterThanOrEqual(0);
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth
  )).toBe(true);
  const accessibility = await new AxeBuilder({page})
    .include(".reasoningMenuRoot")
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(accessibility.violations).toEqual([]);
});

test("requires explicit provider and model selection during setup", async ({page}) => {
  await page.route("**/api/v1/bootstrap", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        protocol_version: 1,
        server_build: "setup-test",
        token: "setup-token",
        ready: false,
        draining: false,
        setup_required: true,
        workspace_root: workspaceDir,
        setup_catalog: {
          version: 1,
          providers: [{
            id: "deepseek",
            display_name: "DeepSeek",
            protocol: "openai_chat",
            requires_api_key: true
          }, {
            id: "openai-compatible",
            display_name: "OpenAI-compatible",
            protocol: "openai_chat",
            requires_api_key: false,
            custom: true
          }]
        }
      })
    });
  });
  await page.goto(baseURL);

  await expect(page.getByRole("heading", {name: "Set up CodeHelper"})).toBeVisible();
  await expect(page.getByLabel("Provider")).toHaveValue("");
  await expect(page.getByRole("button", {name: "Start CodeHelper"})).toBeDisabled();

  await page.getByLabel("Provider").selectOption("deepseek");
  await expect(page.getByLabel("Model ID")).toHaveValue("");
  await page.getByLabel("Model ID").fill("deepseek-reasoner");
  await page.getByLabel("API key").fill("sk-test");
  await expect(page.getByText(/operating system Keyring/)).toBeVisible();
  await expect(page.getByRole("button", {name: "Start CodeHelper"})).toBeEnabled();

  const accessibility = await new AxeBuilder({page})
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(accessibility.violations).toEqual([]);

  await page.getByLabel("Provider").selectOption("openai-compatible");
  await expect(page.getByLabel("Base URL")).toBeVisible();
  await expect(page.getByLabel("Protocol")).toHaveValue("openai_chat");
  await expect(page.getByLabel("Model ID")).toHaveValue("");
  await page.setViewportSize({width: 390, height: 844});
  await expect.poll(() => page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth
  )).toBeLessThanOrEqual(0);
  await expect(page.getByRole("button", {name: "Start CodeHelper"})).toBeVisible();
});

test("passes the WCAG A and AA accessibility scan", async ({page}) => {
  for (const colorScheme of ["light", "dark"] as const) {
    await page.emulateMedia({colorScheme});
    await page.goto(baseURL);
    await expect(page.locator(".app")).toBeVisible();

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

test("groups Sessions by Workspace and reveals row actions on demand", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();

  const workspace = page.locator(".workspaceRow");
  await expect(workspace).toContainText(path.basename(workspaceDir));
  await expect(workspace).toHaveAttribute("aria-expanded", "true");
  const session = page.locator(".sessionRow[data-active]");
  await expect(session).toContainText("New Chat");

  await session.hover();
  await session.getByRole("button", {name: /Session actions for/}).click();
  await expect(session.getByRole("menuitem", {name: "Rename"})).toBeVisible();
  await expect(session.getByRole("menuitem", {name: "Pin"})).toBeVisible();
  await expect(session.getByRole("menuitem", {name: "Archive"})).toBeVisible();
  await expect(session.getByRole("menuitem", {name: "Delete"})).toBeVisible();

  await workspace.click();
  await expect(workspace).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator(".sessionRow")).toHaveCount(0);
  await workspace.click();
  await expect(page.locator(".sessionRow")).toHaveCount(1);
});

test("shows and switches the Workspace Git branch", async ({page}) => {
  execFileSync("git", ["add", "."], {cwd: workspaceDir});
  execFileSync("git", [
    "-c", "user.name=CodeHelper",
    "-c", "user.email=fixture@codehelper.invalid",
    "commit", "-qm", "branch fixture"
  ], {cwd: workspaceDir});
  execFileSync("git", ["branch", "feature"], {cwd: workspaceDir});
  await page.goto(baseURL);

  const branch = page.getByLabel(`Branch for ${path.basename(workspaceDir)}`);
  await expect(branch).toBeVisible();
  await branch.selectOption("feature");
  await expect(branch).toHaveValue("feature");
  expect(execFileSync(
    "git", ["branch", "--show-current"], {cwd: workspaceDir, encoding: "utf8"}
  ).trim()).toBe("feature");
});

test("adds a second Workspace and keeps its Sessions isolated", async ({page}) => {
  const secondary = await mkdtemp(
    path.join(tmpdir(), "codehelper-web-workspace-secondary-")
  );
  try {
    await writeFile(
      path.join(secondary, "README.md"),
      "# Secondary workspace\n"
    );
    execFileSync("git", ["init", "-q"], {cwd: secondary});
    execFileSync("git", ["add", "README.md"], {cwd: secondary});
    await page.goto(baseURL);

    await page.getByRole("button", {name: "Add workspace"}).click();
    await expect(page.getByRole("dialog", {name: "Workspaces"})).toBeVisible();
    await page.getByLabel("Local folder path").fill(secondary);
    await page.getByRole("button", {name: "Open workspace"}).click();

    const primaryGroup = page.locator(".workspaceGroup").filter({
      hasText: path.basename(workspaceDir)
    });
    const secondaryGroup = page.locator(".workspaceGroup").filter({
      hasText: path.basename(secondary)
    });
    await expect(page.locator(".workspaceGroup")).toHaveCount(2);
    await expect(secondaryGroup.locator(".workspaceRow"))
      .toHaveAttribute("data-active", "true");
    await page.getByRole("button", {name: "Create session"}).click();
    await expect(secondaryGroup.locator(".sessionRow")).toHaveCount(1);

    await primaryGroup.locator(".workspaceRow").click();
    await expect(primaryGroup.locator(".workspaceRow"))
      .toHaveAttribute("data-active", "true");
    await page.getByRole("button", {name: "Create session"}).click();
    await expect(primaryGroup.locator(".sessionRow")).toHaveCount(1);
    await expect(secondaryGroup.locator(".sessionRow")).toHaveCount(1);

    await page.setViewportSize({width: 390, height: 844});
    await expect.poll(() => page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth
    )).toBeLessThanOrEqual(0);
  } finally {
    await rm(secondary, {recursive: true, force: true});
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
  await expect(page.locator(".reasoningDisclosure")).toContainText(
    "I should answer briefly."
  );
  await expect(page.getByText("Working", {exact: true})).toHaveCount(0);
  await expect(page.locator(".sessionRow[data-active]")).toContainText("say hello");
});

test("inherits Approval when creating another Session", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await page.getByLabel("Approval").selectOption("auto");
  await expect(page.getByLabel("Approval")).toHaveValue("auto");

  await page.locator('button[aria-label="New chat"]').click();
  await expect(page.getByLabel("Approval")).toHaveValue("auto");
});

test("submits local attachments as verified Runtime context", async ({page}) => {
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();

  const picker = page.locator('input[type="file"][aria-label="Attach files"]');
  await expect(page.locator(
    '.composerControls button[aria-label="Attach files"]'
  )).toBeEnabled();
  await picker.setInputFiles(path.join(workspaceDir, "README.md"));
  await expect(page.getByLabel("Composer attachments")).toContainText(
    "Text"
  );
  await expect(page.getByLabel("Composer attachments")).toContainText(
    "picker"
  );

  const operation = page.waitForRequest((request) =>
    request.url().endsWith("/api/v1/operation/submit") &&
    request.postDataJSON()?.kind === "turn.start"
  );
  await page.getByPlaceholder("Ask CodeHelper").fill("review the attachment");
  await page.getByRole("button", {name: "Send"}).click();
  const payload = await operation;
  expect(payload.postDataJSON()).toMatchObject({
    payload: {
      prompt: "review the attachment",
      context: [{
        kind: "attachment",
        source: "native_picker",
        label: "README.md",
        media_type: "text/plain",
        explicit: true
      }]
    }
  });
  await expect(page.getByLabel("Composer attachments")).toHaveCount(0);
});

test("searches and invokes slash commands entirely from the keyboard", async ({page}) => {
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();

  const composer = page.getByPlaceholder("Ask CodeHelper");
  await composer.fill("/cont");
  const search = page.getByRole("searchbox", {name: "Search commands"});
  await expect(search).toBeFocused();
  await expect(search).toHaveValue("cont");
  await expect(page.getByRole("menuitem", {name: /\/context/})).toBeVisible();
  await expect(page.getByRole("menuitem", {name: /\/compact/})).toHaveCount(0);
  const accessibility = await new AxeBuilder({page})
    .include(".commandMenu")
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  expect(accessibility.violations).toEqual([]);
  await search.press("Enter");

  await expect(page.getByRole("dialog", {name: "Add context"})).toBeVisible();
  await page.getByRole("button", {name: "Close context browser"}).click();
  await page.getByRole("button", {name: "Commands"}).click();
  await expect(page.getByText("Recent", {exact: true})).toBeVisible();
  await expect(page.getByRole("menuitem").first()).toContainText("/context");
});

test("keeps a long mobile draft scrollable above a resized visual viewport", async ({page}) => {
  await page.setViewportSize({width: 390, height: 844});
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();

  const composer = page.getByPlaceholder("Ask CodeHelper");
  await composer.fill(Array.from({length: 120}, (_, index) => `line ${index}`).join("\n"));
  await composer.focus();
  await page.setViewportSize({width: 390, height: 420});

  const geometry = await composer.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return {
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      bottom: box.bottom,
      viewportHeight: window.visualViewport?.height ?? window.innerHeight,
      pageOverflow: document.documentElement.scrollWidth - window.innerWidth
    };
  });
  expect(geometry.clientHeight).toBeLessThanOrEqual(336);
  expect(geometry.scrollHeight).toBeGreaterThan(geometry.clientHeight);
  expect(geometry.bottom).toBeLessThanOrEqual(geometry.viewportHeight + 1);
  expect(geometry.pageOverflow).toBeLessThanOrEqual(0);
});

test("opens the execution trajectory and inspects its event ledger", async ({page}) => {
  await page.goto(baseURL);
  await page.getByRole("button", {name: "Create session"}).click();
  await page.getByPlaceholder("Ask CodeHelper").fill("say hello");
  await page.getByRole("button", {name: "Send"}).click();
  await expect(page.getByText("hello", {exact: true}).last()).toBeVisible();
  await expect(page.locator(".reasoningDisclosure")).toContainText(
    "I should answer briefly."
  );

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
  page.once("dialog", (dialog) => dialog.accept());

  const session = page.locator(".sessionRow").first();
  await session.hover();
  await session.getByRole("button", {name: /Session actions for/}).click();
  await session.getByRole("menuitem", {name: "Delete"}).click();

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
  await expect(page.locator(".reasoningDisclosure")).toContainText(
    "I should answer briefly."
  );
  await expect(page.locator(".sessionRow[data-active]")).toContainText("say hello");

  await page.reload();

  await expect(page.getByText("Connected", {exact: true})).toBeVisible();
  await expect(page.getByText("hello", {exact: true}).last()).toBeVisible();
  await expect(page.locator(".reasoningDisclosure")).toContainText(
    "I should answer briefly."
  );
  await expect(page.locator(".sessionRow[data-active]")).toContainText("say hello");
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
});

test("shows model routing and capabilities in Settings", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await page.getByRole("button", {name: "Settings"}).click();
  await page.getByRole("button", {name: "Connection"}).click();
  await expect(page.getByText("fixture", {exact: true})).toBeVisible();
  await expect(page.getByRole("button", {name: "Test connection"})).toBeVisible();
  await page.getByRole("button", {name: "Models"}).click();

  const model = page.getByLabel("Settings model");
  await expect(model).toHaveValue("fixture-model");
  await page.getByRole("button", {name: "New model"}).click();
  await expect(page.getByRole("button", {name: "Existing models"})).toBeVisible();
  await expect(model).toBeFocused();
  await expect(model).toHaveValue("");
  await expect(page.getByRole("alert")).toHaveText("Model ID is required");
  await expect(page.getByRole("button", {name: "Apply changes"})).toBeDisabled();
  await model.fill("fixture-model-next");
  await page.getByRole("button", {name: "Test model"}).click();
  await expect(page.getByText(
    "Connection succeeded and the provider listed this model"
  )).toBeVisible();
  await page.setViewportSize({width: 390, height: 844});
  await expect.poll(() => page.locator(".settingsDialog").evaluate((dialog) =>
    dialog.scrollWidth <= dialog.clientWidth
  )).toBe(true);
  await page.getByRole("button", {name: "Apply changes"}).click();
  await expect(model).toHaveValue("fixture-model-next");
  await page.reload();
  await page.getByRole("button", {name: "Settings"}).click();
  await page.getByRole("button", {name: "Models"}).click();
  await expect(page.getByLabel("Settings model")).toHaveValue(
    "fixture-model-next"
  );
  await expect(page.locator(
    'select[aria-label="Settings model"] option[value="fixture-model-next"]'
  )).toHaveCount(1);
  await expect(page.getByText("Context window")).toBeVisible();
  await expect(page.getByText("Prompt cache", {exact: true})).toBeVisible();
  await page.getByRole("button", {name: "Close settings"}).click();
  await expect(page.getByLabel("Model")).toHaveValue("fixture-model-next");
});

test("persists and applies a workspace Agent preset", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
  await page.getByRole("button", {name: "Settings"}).click();
  await page.getByRole("button", {name: "Agent preset"}).click();

  await page.getByLabel("Agent mode").selectOption("plan");
  await page.getByLabel("Maximum steps").fill("16");
  await page.getByLabel("Agent preset name").fill("Focused review");
  await page.getByLabel("Agent preset description").fill("Plan with bounded steps");
  await page.getByRole("button", {name: "Save new"}).click();
  const presetStatus = page.locator(".presetWorkbench").getByRole("status");
  await expect(presetStatus).toContainText("Preset created");
  const accessibility = await new AxeBuilder({page})
    .include(".settingsDialog")
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  expect(accessibility.violations).toEqual([]);

  await page.getByRole("button", {name: "Discard"}).click();
  await page.getByRole("button", {name: "Apply to session"}).click();
  await expect(presetStatus).toContainText("Preset applied");
  await expect(page.getByLabel("Agent mode")).toHaveValue("plan");
  await expect(page.getByLabel("Maximum steps")).toHaveValue("16");

  await page.getByRole("button", {name: "Close settings"}).click();
  await page.reload();
  await page.getByRole("button", {name: "Settings"}).click();
  await page.getByRole("button", {name: "Agent preset"}).click();
  await expect(page.getByLabel("Saved agent preset")).toContainText("Focused review");

  await page.setViewportSize({width: 390, height: 844});
  await expect.poll(() => page.locator(".settingsDialog").evaluate((dialog) => {
    const box = dialog.getBoundingClientRect();
    const buttons = Array.from(dialog.querySelectorAll<HTMLElement>("button"))
      .filter((button) => button.offsetParent !== null)
      .map((button) => button.getBoundingClientRect());
    return dialog.scrollWidth <= dialog.clientWidth &&
      box.left >= 0 &&
      box.right <= window.innerWidth &&
      buttons.every(
        (button) => button.left >= box.left && button.right <= box.right
      );
  })).toBe(true);
});

test("browses workspace resources and restores an archived Session", async ({page}) => {
  await page.goto(baseURL);
  await page.locator('button[aria-label="New chat"]').click();
  await expect(page.getByPlaceholder("Ask CodeHelper")).toBeEnabled();
  await openContextDetails(page);

  const fileEntry = page.locator(".contextResults button").filter({
    has: page.getByText("README.md", {exact: true})
  });
  await expect(fileEntry).toBeVisible();
  await fileEntry.click();
  await expect(page.locator(".contextPreview")).toBeVisible();
  const resourceContent = page.getByLabel("Workspace resource content");
  await resourceContent.focus();
  await resourceContent.evaluate((element: HTMLTextAreaElement) => {
    element.setSelectionRange(0, Math.min(5, element.value.length));
    element.dispatchEvent(new Event("select", {bubbles: true}));
    document.dispatchEvent(new Event("selectionchange", {bubbles: true}));
  });
  await page.getByRole("button", {name: "Add selection"}).click();
  await expect(page.getByLabel("Prompt context", {exact: true})).toContainText(
    /:1:1-1:6/
  );
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", {name: "Download resource"}).click()
  ]);
  expect(download.suggestedFilename()).toBe("README.md");

  const symbolSearch = page.getByLabel("Search workspace symbols");
  await symbolSearch.fill("helloFixture");
  await symbolSearch.press("Enter");
  const symbol = page.getByRole("button", {name: /helloFixture.*function.*main.go:3/});
  await expect(symbol).toBeVisible();
  await symbol.click();
  await expect(page.getByLabel("Prompt context", {exact: true})).toContainText("main.go");

  const imageEntry = page.locator(".contextResults button").filter({
    has: page.getByText("diagram.png", {exact: true})
  });
  await imageEntry.click();
  await expect(page.getByRole("img", {name: "diagram.png"})).toBeVisible();
  await page.getByRole("button", {name: "Add image"}).click();
  await expect(page.getByLabel("Prompt context", {exact: true})).toContainText(
    "diagram.png"
  );

  await page.getByRole("button", {name: "Close context browser"}).click();
  let activeSession = page.locator(".sessionRow[data-active]");
  await activeSession.hover();
  await activeSession.getByRole("button", {name: /Session actions for/}).click();
  page.once("dialog", (dialog) => dialog.accept("Archive Target"));
  await activeSession.getByRole("menuitem", {name: "Rename"}).click();
  await expect(page.locator(".sessionRow").filter({
    hasText: "Archive Target"
  })).toBeVisible();

  activeSession = page.locator(".sessionRow[data-active]");
  await activeSession.hover();
  await activeSession.getByRole("button", {name: /Session actions for/}).click();
  page.once("dialog", (dialog) => dialog.accept());
  await activeSession.getByRole("menuitem", {name: "Archive"}).click();
  await expect(page.getByRole("heading", {name: "Archive Target", level: 1})).toHaveCount(0);

  await page.getByRole("button", {name: "Search sessions"}).click();
  await page.getByRole("button", {name: "Show archived"}).click();
  const archived = page.locator(".sessionRow").filter({
    has: page.getByText("Archive Target", {exact: true})
  });
  await archived.locator(".sessionSelect").click();
  await archived.hover();
  await archived.getByRole("button", {name: /Session actions for/}).click();
  await archived.getByRole("menuitem", {name: "Restore"}).click();
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

async function openContextDetails(page: Page): Promise<void> {
  await page.getByRole("button", {name: "Commands"}).click();
  await page.getByRole("menuitem", {name: /context/}).click();
}

function runtimeURL(
  child: ChildProcessByStdio<null, Readable, Readable>
): Promise<string> {
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
      if (match) void workspaceURL(match[1]).then(
        (url) => finish(undefined, url),
        (error: Error) => finish(error)
      );
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

async function workspaceURL(origin: string): Promise<string> {
  const bootstrap = await fetch(new URL("/api/v1/bootstrap", origin));
  const value = await bootstrap.json() as {
    workspace_catalog: {default_workspace_id: string};
  };
  const target = new URL(origin);
  target.searchParams.set(
    "workspace",
    value.workspace_catalog.default_workspace_id
  );
  return target.toString();
}
