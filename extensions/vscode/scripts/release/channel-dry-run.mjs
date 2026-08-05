import { createHash } from "node:crypto";
import {
  copyFile,
  mkdir,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";

const extensionRoot = resolve(import.meta.dirname, "..", "..");
const releaseRoot = resolve(
  process.env["CODEHELPER_VSCODE_RELEASE_OUTPUT"] ??
    join(extensionRoot, "dist", "vscode-release"),
);
const provenance = JSON.parse(await readFile(
  join(releaseRoot, "provenance", "release-provenance.json"),
  "utf8",
));
const channels = JSON.parse(await readFile(
  join(import.meta.dirname, "channels.json"),
  "utf8",
));
if (provenance.schema_version !== 1 || !Array.isArray(provenance.artifacts) ||
  channels.schema_version !== 1) {
  throw new Error("release provenance or channel mapping is invalid");
}
await verifyChecksums(releaseRoot);
const channelRoot = join(releaseRoot, "channels");
await rm(channelRoot, { recursive: true, force: true });
const marketplace = [];
const openVSX = [];
for (const artifact of provenance.artifacts) {
  const path = join(releaseRoot, artifact.file);
  if (await digest(path) !== artifact.sha256) {
    throw new Error(`provenance mismatch for ${artifact.file}`);
  }
  marketplace.push({
    artifact: artifact.file,
    target: artifact.target,
    command: [
      "npx", "vsce", "publish",
      "--packagePath", artifact.file,
      "--no-dependencies",
    ],
  });
  openVSX.push({
    artifact: artifact.file,
    target: artifact.target,
    command: [
      "npx", "ovsx", "publish",
      artifact.file,
    ],
  });
}
await writeJSON(join(channelRoot, "marketplace", "publication.json"), {
  schema_version: 1,
  channel: "marketplace",
  publisher: channels.marketplace.publisher,
  dry_run: provenance.dry_run === true,
  uploaded: false,
  credential_source: "VSCE_PAT or federated CI identity",
  plans: marketplace,
});
await writeJSON(join(channelRoot, "open-vsx", "publication.json"), {
  schema_version: 1,
  channel: "open-vsx",
  namespace: channels.open_vsx.namespace,
  dry_run: provenance.dry_run === true,
  uploaded: false,
  credential_source: "OVSX_PAT or federated CI identity",
  plans: openVSX,
});
for (const channel of ["enterprise", "offline"]) {
  const root = join(channelRoot, channel);
  await mkdir(join(root, "artifacts"), { recursive: true });
  for (const artifact of provenance.artifacts) {
    await copyFile(
      join(releaseRoot, artifact.file),
      join(root, "artifacts", basename(artifact.file)),
    );
  }
  await mkdir(join(root, "provenance"), { recursive: true });
  await copyFile(
    join(releaseRoot, "provenance", "release-provenance.json"),
    join(root, "provenance", "release-provenance.json"),
  );
  await copyFile(
    join(releaseRoot, "provenance", "release-manifest.json"),
    join(root, "provenance", "release-manifest.json"),
  );
  await copyFile(
    join(releaseRoot, "provenance", "release-trust-roots.json"),
    join(root, "provenance", "release-trust-roots.json"),
  );
  const sbomFile = `codehelper-vscode-${provenance.extension_version}.cdx.json`;
  await mkdir(join(root, "sbom"), { recursive: true });
  await copyFile(
    join(releaseRoot, "sbom", sbomFile),
    join(root, "sbom", sbomFile),
  );
  await writeJSON(join(root, "publication.json"), {
    schema_version: 1,
    channel,
    dry_run: provenance.dry_run === true,
    uploaded: false,
    layout: channels[channel].layout,
    artifacts: provenance.artifacts.map((artifact) =>
      `artifacts/${basename(artifact.file)}`),
  });
  await writeChecksums(root);
}
process.stdout.write(
  `channel dry-run complete; uploaded=false; root=${channelRoot}\n`,
);

async function verifyChecksums(root) {
  const lines = (await readFile(join(root, "SHA256SUMS"), "utf8"))
    .trim().split("\n");
  for (const line of lines) {
    const match = /^([0-9a-f]{64}) {2}([^\r\n]+)$/u.exec(line);
    if (match === null) throw new Error("SHA256SUMS is malformed");
    const expected = match[1];
    const file = match[2];
    if (expected === undefined || file === undefined ||
      file.startsWith("/") || file.split("/").includes("..") ||
      await digest(join(root, file)) !== expected) {
      throw new Error(`SHA256SUMS mismatch for ${file ?? "unknown"}`);
    }
  }
}

async function digest(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function writeJSON(path, value) {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

async function writeChecksums(root) {
  const entries = [];
  for (const path of await regularFiles(root)) {
    if (basename(path) === "SHA256SUMS") continue;
    entries.push(`${await digest(path)}  ${path.slice(root.length + 1)}`);
  }
  entries.sort();
  await writeFile(join(root, "SHA256SUMS"), `${entries.join("\n")}\n`, {
    mode: 0o600,
  });
}

async function regularFiles(root) {
  const output = [];
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) output.push(...await regularFiles(path));
    if (entry.isFile()) output.push(path);
  }
  return output;
}
