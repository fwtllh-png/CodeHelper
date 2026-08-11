export const journeyEvidence = Object.freeze([
  automated("empty", "Empty workspace activation"),
  automated("context.native", "All native Add Context capture types"),
  automated("resource.navigation", "Native resource open and side-by-side"),
  automated("theme.light-dark-high-contrast", "Webview theme propagation"),
  automated("forced-colors", "Chromium forced-colors media query"),
  automated("zoom.200", "Approximately 200% workbench zoom"),
  automated("ime.composition", "Bundled Webview IME composition guard"),
  automated("hidden.resume", "Hidden projection suspension and resume"),
  automated("runtime.streaming-stop", "Streaming Turn cancellation"),
  automated("runtime.retry-continue", "Retry and Continue create new Turns"),
  automated("composer.model-thinking", "Model catalog and Thinking Profile"),
  automated("composer.tools", "Tool catalog and allowlist persistence"),
  automated("composer.credential", "Credential validation without secret leakage"),
  automated("approval-verification-receipt", "Approval, verification, and receipt"),
  automated("session.lifecycle", "Session create, switch, fork, archive, delete"),
  automated("session.search-to-turn", "Durable search match with stable Turn ID"),
  automated("plan.destinations", "Current, new Session, and Checkpoint Fork"),
  automated("workspace.multi-root", "Local multi-root routing and recovery"),
  automated("surface.webview-view", "Single movable WebviewView contribution"),
  manual(
    "surface.panel-move",
    "Move the same Chat WebviewView from Sidebar to Panel",
  ),
]);

export const requiredAutomatedJourneyIDs = Object.freeze(
  journeyEvidence
    .filter((journey) => journey.kind === "automated")
    .map((journey) => journey.id),
);

function automated(id, description) {
  return Object.freeze({ id, description, kind: "automated" });
}

function manual(id, description) {
  return Object.freeze({ id, description, kind: "manual" });
}
