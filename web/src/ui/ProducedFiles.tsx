import {
  CheckCircle2,
  ChevronDown,
  Download,
  ExternalLink,
  FilePenLine,
  FilePlus2,
  FileX2,
  GitCompareArrows,
  ScanSearch,
  ShieldAlert
} from "lucide-react";
import {useState} from "react";

import type {ConversationNode, ProjectedDeliverable} from "../projection/conversation";
import type {RuntimeClient} from "../runtime/client";
import {EditPlanPreview} from "./TranscriptCards";

type DeliverablesNode = Extract<ConversationNode, {kind: "deliverables"}>;

export function ProducedFiles({
  entry,
  client,
  canOpenPath,
  onInspect,
  onError
}: {
  entry: DeliverablesNode;
  client: RuntimeClient;
  canOpenPath: boolean;
  onInspect: (callID: string) => void;
  onError: (error: unknown) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const [diffPath, setDiffPath] = useState("");
  const [workspaceDiff, setWorkspaceDiff] = useState("");
  const [loadingDiff, setLoadingDiff] = useState(false);
  const groups = groupFiles(entry.files);

  const download = async (file: ProjectedDeliverable) => {
    try {
      const resource = await client.readWorkspaceResource(file.path)
        .catch(() => client.readWorkspaceImage(file.path));
      const blob = await client.downloadWorkspaceContent(resource.content_handle);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = file.path.split("/").at(-1) || "download";
      anchor.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      onError(error);
    }
  };

  const showDiff = async (file: ProjectedDeliverable) => {
    if (diffPath === file.path) {
      setDiffPath("");
      setWorkspaceDiff("");
      return;
    }
    setDiffPath(file.path);
    setWorkspaceDiff("");
    if (file.diff) return;
    setLoadingDiff(true);
    try {
      const value = await client.workspaceDiff();
      setWorkspaceDiff(value.diff);
    } catch (error) {
      onError(error);
    } finally {
      setLoadingDiff(false);
    }
  };

  return (
    <section className="producedFiles" aria-label="Produced files">
      <button
        type="button"
        className="producedFilesSummary"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        <FilePenLine size={15} />
        <span>Produced files</span>
        <small>Workspace · {entry.files.length}</small>
        <span className="deliverableVerification" data-state={entry.verification}>
          {entry.verification === "passed"
            ? <CheckCircle2 size={12} />
            : <ShieldAlert size={12} />}
          {entry.verification}
        </span>
        <ChevronDown size={15} data-expanded={expanded || undefined} />
      </button>
      {expanded && (
        <div className="producedFilesBody">
          {groups.map(([kind, files]) => (
            <div className="producedFileGroup" key={kind}>
              <div className="producedFileGroupTitle">
                {changeIcon(kind)}
                <span>{kind}</span>
                <small>{files.length}</small>
              </div>
              {files.map((file) => (
                <div
                  className="producedFile"
                  key={file.path}
                  data-file-path={file.path}
                  data-stale={file.stale || undefined}
                >
                  <button
                    type="button"
                    className="producedFilePath"
                    disabled={!canOpenPath || file.kind === "deleted"}
                    title={file.stale ? "Open current workspace version" : "Open in local editor"}
                    onClick={() => void client.openWorkspacePath(file.path).catch(onError)}
                  >
                    {file.path}
                  </button>
                  <span className="producedFileDelta">
                    +{file.added} -{file.removed}
                  </span>
                  {file.stale && <span className="producedFileStale">stale</span>}
                  <div className="producedFileActions">
                    {canOpenPath && file.kind !== "deleted" && (
                      <button
                        type="button"
                        aria-label={`Open ${file.path}`}
                        title="Open in local editor"
                        onClick={() => void client.openWorkspacePath(file.path).catch(onError)}
                      >
                        <ExternalLink size={13} />
                      </button>
                    )}
                    {file.kind !== "deleted" && (
                      <button
                        type="button"
                        aria-label={`Download ${file.path}`}
                        title={file.stale ? "Download current version" : "Download"}
                        onClick={() => void download(file)}
                      >
                        <Download size={13} />
                      </button>
                    )}
                    <button
                      type="button"
                      aria-label={`View diff for ${file.path}`}
                      title={file.diff ? "View turn diff" : "View current workspace diff"}
                      onClick={() => void showDiff(file)}
                    >
                      <GitCompareArrows size={13} />
                    </button>
                    {file.callID && (
                      <button
                        type="button"
                        aria-label={`Inspect tool for ${file.path}`}
                        title="Inspect producing tool"
                        onClick={() => onInspect(file.callID ?? "")}
                      >
                        <ScanSearch size={13} />
                      </button>
                    )}
                  </div>
                  {file.summary && <small className="producedFileNote">{file.summary}</small>}
                  {diffPath === file.path && (
                    <div className="producedFileDiff">
                      {file.diff ? (
                        <EditPlanPreview files={[file.diff]} diff="" />
                      ) : loadingDiff ? (
                        <span>Loading current workspace diff...</span>
                      ) : workspaceDiff ? (
                        <>
                          <small>Current workspace diff</small>
                          <pre>{workspaceDiff}</pre>
                        </>
                      ) : (
                        <span>No current workspace diff is available.</span>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function groupFiles(files: readonly ProjectedDeliverable[]) {
  const order = ["created", "modified", "deleted"];
  const groups = new Map<string, ProjectedDeliverable[]>();
  for (const file of files) {
    const values = groups.get(file.kind) ?? [];
    values.push(file);
    groups.set(file.kind, values);
  }
  return [...groups].sort(([left], [right]) =>
    rank(left, order) - rank(right, order)
  );
}

function rank(value: string, order: readonly string[]): number {
  const index = order.indexOf(value);
  return index < 0 ? order.length : index;
}

function changeIcon(kind: string) {
  if (kind === "created") return <FilePlus2 size={13} />;
  if (kind === "deleted") return <FileX2 size={13} />;
  return <FilePenLine size={13} />;
}
