import {expect, test, type Locator, type Page} from "@playwright/test";
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
  await page.getByRole("button", {name: "Connection"}).click();
  await expect(page.getByRole("button", {name: "Test connection"})).toBeVisible();
  await expect(page.getByText("Runtime-managed")).toBeVisible();
  await expect(page).toHaveScreenshot("canonical-settings-connection.png");

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
  const produced = page.getByRole("region", {name: "Produced files"});
  await expect(produced).toContainText("README.md");
  await expect(produced).toContainText("passed");
  await page.getByRole("button", {name: "View diff for README.md"}).click();
  await expect(produced.locator("[data-diff]")).toContainText("README.md");
  await expect(page).toHaveScreenshot("canonical-diff.png");
  await page.setViewportSize({width: 390, height: 844});
  await expect(produced).toBeVisible();
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth
  )).toBe(true);
  await page.getByRole("button", {name: "Inspect tool for README.md"}).click();
  await expect(page.getByRole("button", {name: "Trajectory"}))
    .toHaveAttribute("aria-current", "page");
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

test("steers the active turn without hiding the stop action", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual long streaming");
  await expect(page.getByRole("button", {name: "Stop turn"})).toBeVisible();

  const composer = page.getByPlaceholder("Ask CodeHelper");
  await composer.fill("Focus on the final verification");
  await page.getByRole("button", {name: "Steer current turn"}).click();

  const steering = page.locator(".userMessage[data-steering]");
  await expect(steering).toContainText("Focus on the final verification");
  await expect(composer).toHaveValue("");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Review complete. Runtime evidence is consistent.");
});

test("queues a follow-up and advances it after the active turn", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual queue");
  await expect(page.getByRole("button", {name: "Stop turn"})).toBeVisible();

  const composer = page.getByPlaceholder("Ask CodeHelper");
  await composer.fill("Verify the queued follow-up");
  await page.getByRole("button", {name: "Queue next"}).click();

  await expect(page.getByText("1 queued message")).toBeVisible();
  await expect(page.locator(".userMessage").filter({
    hasText: "Verify the queued follow-up"
  })).toBeVisible();
  await expect(page.getByText("1 queued message")).not.toBeVisible();
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Review complete. Runtime evidence is consistent.");
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

test("renders rich Markdown without stretching the conversation", async ({page}) => {
  await createSession(page);
  await submitPrompt(page, "visual rich content");
  await expect(page.getByRole("heading", {name: "Rich content"})).toBeVisible();

  const message = page.locator(".assistantMessage").last();
  await expect(message.locator("strong")).toContainText("注意：");
  await expect(message.locator(".katex")).toHaveCount(2);
  await expect(message.locator('math[display="block"]')).toHaveCount(1);
  await expect(message.getByRole("button", {
    name: "Open file README.md"
  })).toBeVisible();

  const image = message.getByRole("img", {name: "CodeHelper mark"});
  await image.scrollIntoViewIfNeeded();
  await expect.poll(() => image.evaluate(
    (element: HTMLImageElement) => element.naturalWidth
  )).toBeGreaterThan(0);
  await expect(message.getByRole("link", {
    name: "Download image CodeHelper mark"
  })).toBeVisible();
  await expect(message.getByRole("alert")).toContainText("Image unavailable");
  await expect(message.getByRole("button", {
    name: "Retry image Missing diagram"
  })).toBeVisible();

  const table = message.getByRole("region", {name: "Response table"});
  const code = message.locator(".markdownCodeBlock pre");
  await expect(table).toBeVisible();
  await expect(code).toBeVisible();
  expect(await table.evaluate(
    (element) => element.scrollWidth > element.clientWidth
  )).toBe(true);
  expect(await code.evaluate(
    (element) => element.scrollWidth > element.clientWidth
  )).toBe(true);
  await expect(message.getByText("Nested item")).toBeVisible();

  const accessibility = await new AxeBuilder({page})
    .include(".assistantMessage")
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  expect(accessibility.violations).toEqual([]);
  await page.setViewportSize({width: 1440, height: 1200});
  await page.locator(".conversationScrollport").evaluate((element) => {
    element.scrollTop = 0;
  });
  await expect(page).toHaveScreenshot("canonical-rich-content.png");

  await page.setViewportSize({width: 390, height: 844});
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth
  )).toBe(true);
});

test("navigates long conversations by stable semantic anchors", async ({page}) => {
  await page.setViewportSize({width: 1200, height: 760});
  await createSession(page);
  for (const suffix of ["first", "second", "third", "fourth"]) {
    const completed = page.locator(".assistantMessage");
    const before = await completed.count();
    await submitPrompt(page, `visual navigation ${suffix}`);
    await expect(completed).toHaveCount(before + 1);
  }

  await expect(page.getByRole("button", {name: "Search conversation"}))
    .toContainText("4/4");
  await page.getByRole("button", {name: "Search conversation"}).click();
  const navigator = page.getByRole("dialog", {name: "Search conversation"});
  await expect(navigator).toBeVisible();
  await expect(navigator.getByRole("option")).not.toHaveCount(0);
  const accessibility = await new AxeBuilder({page})
    .include(".conversationNavigator")
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  expect(accessibility.violations).toEqual([]);
  await expect(page).toHaveScreenshot("canonical-conversation-navigator.png");

  await navigator.getByRole("tab", {name: "Files"}).click();
  await navigator.getByRole("combobox", {name: "Search conversation"})
    .fill("main.go");
  await navigator.getByRole("option").first().click();
  const selected = page.locator(
    ".transcriptEntryAnchor[data-navigation-current]"
  );
  await expect(selected.locator(".toolDisclosure")).toBeVisible();
  await expect(page.getByRole("button", {name: "Search conversation"}))
    .toContainText("1/4");

  await page.getByRole("button", {name: "Next user question"}).click();
  await expect(
    page.locator(".transcriptEntryAnchor[data-navigation-current] .userMessage")
  ).toContainText("visual navigation second");
  await expect(page.getByRole("button", {name: "Search conversation"}))
    .toContainText("2/4");
  await page.getByRole("button", {name: "Next user question"}).click();
  const highlightedThird = page.locator(
    ".transcriptEntryAnchor[data-navigation-current]"
  );
  await expect(highlightedThird.locator(".userMessage"))
    .toContainText("visual navigation third");
  const thirdQuestion = page.locator(".transcriptEntryAnchor")
    .filter({hasText: "visual navigation third"});

  const anchorBefore = await transcriptAnchorTop(thirdQuestion);
  await page.getByRole("button", {name: "Trajectory"}).click();
  await expect(page.getByLabel("Execution trajectory")).toBeVisible();
  await page.getByRole("button", {name: "Chat", exact: true}).click();
  await expect.poll(
    async () => Math.abs(
      (await transcriptAnchorTop(thirdQuestion)) - anchorBefore
    )
  ).toBeLessThanOrEqual(2);

  const beforeToolExpansion = await transcriptAnchorTop(thirdQuestion);
  const firstTool = page.locator(".toolDisclosure .disclosureRow").first();
  await firstTool.evaluate((button: HTMLButtonElement) => button.click());
  await expect(firstTool).toHaveAttribute("aria-expanded", "true");
  await expect.poll(
    async () => Math.abs(
      (await transcriptAnchorTop(thirdQuestion)) - beforeToolExpansion
    )
  ).toBeLessThanOrEqual(2);

  const beforeSessionSwitch = await transcriptAnchorTop(thirdQuestion);
  await page.getByRole("button", {name: "New chat"}).click();
  await expect(page.locator(".sessionRow")).toHaveCount(2);
  await page.locator(".sessionSelect")
    .filter({hasText: "visual navigation first"})
    .click();
  await expect(thirdQuestion).toBeVisible();
  await expect.poll(
    async () => Math.abs(
      (await transcriptAnchorTop(thirdQuestion)) - beforeSessionSwitch
    )
  ).toBeLessThanOrEqual(2);

  await page.setViewportSize({width: 390, height: 844});
  await page.getByRole("button", {name: "Search conversation"}).click();
  await expect(page.getByRole("dialog", {name: "Search conversation"}))
    .toBeVisible();
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth
  )).toBe(true);
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
  await expect(page.getByText(/Side effects:/)).toBeVisible();
  await expect(page.getByRole("button", {name: "Continue"})).toBeVisible();
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

test("keeps background work visible and opens its completion notification", async ({page}) => {
  await page.addInitScript(() => {
    type CapturedNotification = {
      title: string;
      body: string;
      onclick: ((event: Event) => unknown) | null;
    };
    const captured: CapturedNotification[] = [];
    Object.assign(window, {__codehelperNotifications: captured});
    class TestNotification {
      static permission: NotificationPermission = "granted";
      static requestPermission = async () => "granted" as NotificationPermission;
      onclick: ((event: Event) => unknown) | null = null;
      onclose: ((event: Event) => unknown) | null = null;

      constructor(
        readonly title: string,
        readonly options?: NotificationOptions
      ) {
        captured.push({
          title,
          body: options?.body ?? "",
          onclick: (event) => this.onclick?.(event)
        });
      }

      close(): void {
        this.onclose?.(new Event("close"));
      }
    }
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: TestNotification
    });
  });
  await page.reload();
  await expect(page.locator(".app")).toBeVisible();

  await page.getByRole("button", {name: "Settings"}).click();
  const notifications = page.getByRole("switch", {name: "Desktop notifications"});
  await expect(notifications).not.toBeChecked();
  await notifications.check();
  await expect(notifications).toBeChecked();
  await page.getByRole("button", {name: "Close settings"}).click();

  await createSession(page);
  await submitPrompt(page, "visual long streaming");
  await expect(page.getByText("Working", {exact: true})).toBeVisible();
  await expect(page).toHaveTitle("(1) Working · CodeHelper");
  await createSession(page);

  const background = page.locator(".sessionRow").filter({
    hasText: "visual long streaming"
  });
  await expect(background.locator('[title="Completed"]')).toBeVisible();
  const captured = await page.evaluate(() => (
    (window as unknown as {
      __codehelperNotifications: Array<{title: string; body: string}>;
    }).__codehelperNotifications
  ));
  expect(captured).toEqual([{
    title: "CodeHelper task completed",
    body: "A background Session completed."
  }]);
  expect(JSON.stringify(captured)).not.toContain("visual long streaming");

  await page.evaluate(() => {
    const notification = (window as unknown as {
      __codehelperNotifications: Array<{
        onclick: ((event: Event) => unknown) | null;
      }>;
    }).__codehelperNotifications.at(-1);
    notification?.onclick?.(new Event("click"));
  });
  await expect(background).toHaveAttribute("data-active", "true");
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Review complete. Runtime evidence is consistent.");

  await createSession(page);
  await submitPrompt(page, "visual background approval");
  await expect(page.getByText("Working", {exact: true})).toBeVisible();
  await createSession(page);
  const approvalBackground = page.locator(".sessionRow").filter({
    hasText: "visual background approval"
  });
  await expect(
    approvalBackground.locator('[title="Approval required"]')
  ).toBeVisible();
  await expect(page).toHaveTitle("(1) Action required · CodeHelper");
  await expect.poll(() => page.evaluate(() => (
    (window as unknown as {
      __codehelperNotifications: Array<{title: string}>;
    }).__codehelperNotifications.length
  ))).toBe(2);
  const approvalNotice = await page.evaluate(() => (
    (window as unknown as {
      __codehelperNotifications: Array<{title: string; body: string}>;
    }).__codehelperNotifications.at(-1)
  ));
  expect(approvalNotice).toEqual({
    title: "CodeHelper needs approval",
    body: "A background Session is waiting for approval."
  });
  await expect(page).toHaveScreenshot("canonical-background-activity.png");

  await page.evaluate(() => {
    const notification = (window as unknown as {
      __codehelperNotifications: Array<{
        onclick: ((event: Event) => unknown) | null;
      }>;
    }).__codehelperNotifications.at(-1);
    notification?.onclick?.(new Event("click"));
  });
  await expect(approvalBackground).toHaveAttribute("data-active", "true");
  await expect(page.getByText("Waiting for approval")).toBeVisible();
  await expect(page.getByLabel("Approval details")).toBeFocused();
  await page.getByRole("button", {name: "Approve once"}).click();
  await expect(page.locator(".assistantMessage").last())
    .toContainText("Approved command completed.");
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

async function transcriptAnchorTop(
  anchor: Locator
): Promise<number> {
  return anchor.evaluate((element) => {
    const scrollport = element.closest<HTMLElement>("[data-conversation-scroll]");
    const content = element.firstElementChild;
    if (!scrollport || !(content instanceof HTMLElement)) {
      throw new Error("Conversation anchor is not mounted");
    }
    return content.getBoundingClientRect().top -
      scrollport.getBoundingClientRect().top;
  });
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
