import {cleanup, fireEvent, render, screen, waitFor} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";
import type {RuntimeEvent, SessionSummary} from "../protocol";
import type {RuntimeClient, RuntimeSnapshot} from "../runtime/client";
import {App, projectTranscript, selectionRange} from "./App";

Object.defineProperty(HTMLElement.prototype, "scrollTo", {
  configurable: true,
  value: vi.fn()
});
Object.defineProperty(URL, "createObjectURL", {
  configurable: true,
  value: vi.fn(() => "blob:workspace-resource")
});
Object.defineProperty(URL, "revokeObjectURL", {
  configurable: true,
  value: vi.fn()
});

afterEach(cleanup);

describe("selectionRange", () => {
  it("converts browser offsets into zero-based protocol positions", () => {
    expect(selectionRange("alpha\nbeta\ngamma", 2, 9)).toEqual({
      start: {line: 0, character: 2},
      end: {line: 1, character: 3}
    });
    expect(selectionRange("alpha", 3, 3)).toBeUndefined();
  });
});

describe("projectTranscript", () => {
  it("reconciles streamed output with the authoritative terminal text", () => {
    const entries = projectTranscript([
      event(1, "output.delta", {text: "draft"}),
      event(2, "turn.completed", {text: "final", outcome: "answered"})
    ]);

    expect(entries).toMatchObject([
      {type: "assistant", text: "final"},
      {type: "status", title: "Completed"}
    ]);
  });

  it("does not duplicate a complete tool result after streamed chunks", () => {
    const entries = projectTranscript([
      event(1, "tool.start", {call_id: "call", tool: "read", arguments: {path: "a.go"}}),
      event(2, "tool.output", {call_id: "call", chunk: "content"}),
      event(3, "tool.result", {call_id: "call", output: "content", is_error: false})
    ]);

    expect(entries).toMatchObject([
      {
        type: "tool",
        title: "read",
        text: "content",
        failed: false,
        callID: "call",
        contextText: "content"
      }
    ]);
  });

  it("projects verification, receipt, and rejection evidence", () => {
    const entries = projectTranscript([
      event(1, "turn.verification", {verdict: "passed"}),
      event(2, "turn.receipt", {outcome: "changed"}),
      event(3, "operation.rejected", {message: "stale request"})
    ]);

    expect(entries).toMatchObject([
      {type: "status", title: "Verification", text: "passed", failed: false},
      {type: "status", title: "Receipt", text: "changed", failed: false},
      {type: "status", title: "Rejected", text: "stale request", failed: true}
    ]);
  });

  it("retains the source turn for failed and canceled recovery actions", () => {
    const entries = projectTranscript([
      event(1, "turn.failed", {message: "provider unavailable"}),
      event(2, "turn.canceled", {reason: "interrupted"})
    ]);

    expect(entries).toMatchObject([
      {type: "status", title: "Failed", failed: true, turnID: "turn"},
      {type: "status", title: "Canceled", failed: true, turnID: "turn"}
    ]);
  });

  it("renders lifecycle, workspace, profile, and governed tool controls", () => {
    const client = mockClient(snapshot());
    render(<App client={client} />);

    expect(screen.getByLabelText("New session isolation")).toBeTruthy();
    expect(screen.getByRole("button", {name: "Browse workspace"})).toBeTruthy();
    expect(screen.getByLabelText("Mode")).toBeTruthy();
    expect(screen.getByLabelText("Provider")).toBeTruthy();
    expect(screen.getByRole("option", {name: "Offline (Credential missing)"}))
      .toHaveProperty("disabled", true);
    fireEvent.change(screen.getByLabelText("Model"), {
      target: {value: "reasoner"}
    });
    expect(client.updateProfile).toHaveBeenCalledWith({model: "reasoner"});
    expect(screen.getAllByRole("button", {name: "Archive session"})).not.toHaveLength(0);
    expect(screen.getByText("read_file")).toBeTruthy();
  });

  it("changes provider and model as one valid catalog route", () => {
    const value = snapshot();
    value.providers = [
      ...value.providers,
      {
        id: "second",
        display_name: "Second",
        selected: false,
        availability: "available"
      }
    ];
    value.models = [
      ...value.models,
      {
        provider: "second",
        id: "second-default",
        selected: true,
        capabilities: modelCapabilities("Second Default")
      }
    ];
    const client = mockClient(value);
    render(<App client={client} />);

    fireEvent.change(screen.getByLabelText("Provider"), {
      target: {value: "second"}
    });

    expect(client.updateProfile).toHaveBeenCalledWith({
      provider: "second",
      model: "second-default"
    });
  });

  it("renders detailed activity artifacts and extension controls", () => {
    const value = snapshot();
    value.tasks = [{
      id: "task-1",
      kind: "verification",
      state: "failed",
      failure_reason: "tests failed"
    }];
    value.agents = [{
      id: "agent-1",
      role: "reviewer",
      status: "working",
      last_message: "reviewing diff"
    }];
    value.usage = {
      turns: 2,
      calls: 3,
      total_tokens: 144,
      cost_microunits: 0,
      cost_known: false
    };
    value.plan = {
      version: 1,
      id: "plan-1",
      session_id: "session",
      thread_id: "thread",
      turn_id: "turn",
      cursor: 4,
      status: "ready",
      body: "Implement the verified change",
      profile_revision: 1,
      can_implement: true,
      can_autopilot: false,
      created_at: "2026-01-01T00:00:00Z"
    };
    value.checkpoints = [{
      version: 1,
      id: "checkpoint-1",
      session_id: "session",
      thread_id: "thread",
      turn_id: "turn",
      cursor: 3,
      status: "completed",
      summary: "Before implementation",
      profile_revision: 1,
      changed_files: 1,
      external_side_effects: false,
      can_restore: true,
      can_fork: true,
      created_at: "2026-01-01T00:00:00Z"
    }];
    value.extensions = [{
      kind: "plugin",
      name: "review-tools",
      enabled: true,
      health: "ready"
    }];
    const client = mockClient(value);
    render(<App client={client} />);

    expect(screen.getByLabelText("Tasks").textContent).toContain(
      "verificationtask-1failedtests failed"
    );
    expect(screen.getByLabelText("Agents").textContent).toContain(
      "revieweragent-1workingreviewing diff"
    );
    expect(screen.getByLabelText("Usage").textContent).toContain(
      "Turns2Calls3CostUnpriced"
    );
    expect(screen.getByText("Implement the verified change")).toBeTruthy();
    expect(screen.getByText("Before implementation")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", {name: "Implement"}));
    fireEvent.click(screen.getByRole("button", {name: "Restore checkpoint"}));
    fireEvent.click(screen.getByRole("button", {name: "Fork checkpoint"}));
    fireEvent.click(screen.getByRole("checkbox", {name: /review-tools/}));

    expect(client.transitionPlan).toHaveBeenCalledWith("implement");
    expect(client.restoreCheckpoint).toHaveBeenCalledWith("checkpoint-1");
    expect(client.forkCheckpoint).toHaveBeenCalledWith("checkpoint-1");
    expect(client.setExtensionEnabled).toHaveBeenCalledWith(
      "plugin",
      "review-tools",
      false
    );
  });

  it("adds a server-issued workspace diff to prompt context", async () => {
    const client = mockClient(snapshot());
    const diff = {
      session_id: "session",
      thread_id: "thread",
      diff: "diff --git a/main.go b/main.go\n",
      digest: "b".repeat(64)
    };
    vi.mocked(client.workspaceDiff).mockResolvedValue(diff);
    render(<App client={client} />);

    fireEvent.click(screen.getByRole("button", {name: "Refresh diff"}));
    await screen.findByText(/diff --git/);
    fireEvent.click(screen.getByRole("button", {name: "Add diff"}));

    expect(client.addGitDiffContext).toHaveBeenCalledWith(diff);
  });

  it("selects server-issued symbol and diagnostic contexts", async () => {
    const client = mockClient(snapshot());
    const symbol = {
      path: "main.go",
      name: "Serve",
      kind: "function",
      line: 3,
      uri: "file:///workspace/main.go",
      document_version: 1,
      digest: "a".repeat(64),
      range: {
        start: {line: 2, character: 0},
        end: {line: 2, character: 15}
      },
      selection_range: {
        start: {line: 2, character: 5},
        end: {line: 2, character: 10}
      }
    };
    const diagnostic = {
      call_id: "call-1",
      tool: "exec_command",
      status: "failed",
      context: {
        kind: "diagnostics" as const,
        source: "code_action" as const,
        uri: "file:///workspace/main.go",
        path: "main.go",
        document_version: 1,
        digest: "a".repeat(64),
        diagnostics: [{
          range: {
            start: {line: 2, character: 0},
            end: {line: 2, character: 6}
          },
          severity: "error" as const,
          message: "broken"
        }],
        explicit: true as const
      }
    };
    vi.mocked(client.searchWorkspaceSymbols).mockResolvedValue({
      query: "Serve",
      status: "ready",
      symbols: [symbol]
    });
    vi.mocked(client.workspaceDiagnostics).mockResolvedValue({
      session_id: "session",
      thread_id: "thread",
      diagnostics: [diagnostic]
    });
    render(<App client={client} />);

    fireEvent.change(screen.getByLabelText("Search workspace symbols"), {
      target: {value: "Serve"}
    });
    fireEvent.submit(screen.getByLabelText("Search workspace symbols").closest("form")!);
    fireEvent.click(await screen.findByRole("button", {name: /Serve/}));
    expect(client.addSymbolContext).toHaveBeenCalledWith(symbol);

    fireEvent.click(screen.getByRole("button", {name: "Refresh diagnostics"}));
    fireEvent.click(await screen.findByRole("button", {name: /main.go.*failed/}));
    expect(client.addDiagnosticsContext).toHaveBeenCalledWith(diagnostic);
  });

  it("previews and selects a validated workspace image", async () => {
    const client = mockClient(snapshot());
    const image = {
      path: "diagram.png",
      uri: "file:///workspace/diagram.png",
      document_version: 1,
      digest: "c".repeat(64),
      bytes: 16,
      media_type: "image/png" as const,
      label: "diagram.png",
      content_handle: "signed-image-handle"
    };
    vi.mocked(client.browseWorkspace).mockResolvedValue({
      path: ".",
      entries: [{path: "diagram.png", kind: "file", size: 16}],
      more: false
    });
    vi.mocked(client.readWorkspaceImage).mockResolvedValue(image);
    vi.mocked(client.downloadWorkspaceContent).mockResolvedValue(
      new Blob(["image"], {type: "image/png"})
    );
    render(<App client={client} />);

    fireEvent.click(screen.getByRole("button", {name: "Browse workspace"}));
    fireEvent.click(await screen.findByRole("button", {name: /diagram.png/}));
    expect(
      (await screen.findByRole("img", {name: "diagram.png"})).getAttribute("src")
    ).toBe("blob:workspace-resource");
    fireEvent.click(screen.getByRole("button", {name: "Add image to prompt context"}));
    expect(client.addImageContext).toHaveBeenCalledWith(image);
  });

  it("adds only a completed tool result to prompt context", async () => {
    const value = snapshot();
    value.events = [
      event(1, "tool.start", {
        call_id: "call-1",
        tool: "exec_command",
        arguments: {cmd: "go test ./..."}
      }),
      event(2, "tool.result", {
        call_id: "call-1",
        tool: "exec_command",
        output: "ok",
        is_error: false
      })
    ];
    const client = mockClient(value);
    render(<App client={client} />);

    fireEvent.click(screen.getByRole("button", {
      name: "Add tool output to prompt context"
    }));

    await waitFor(() => {
      expect(client.addTerminalContext).toHaveBeenCalledWith("call-1", "ok");
    });
  });

  it("keeps credential values in the keyring control flow", async () => {
    const status = {
      reference: {kind: "keyring", name: "codehelper"},
      configured: true,
      validation: "valid" as const
    };
    const client = mockClient(snapshot());
    vi.mocked(client.credentialStatus).mockResolvedValue(status);
    vi.mocked(client.setKeyringCredential).mockResolvedValue(status);
    vi.mocked(client.validateCredential).mockResolvedValue(status);
    vi.mocked(client.clearKeyringCredential).mockResolvedValue({
      ...status,
      configured: false
    });
    render(<App client={client} />);

    fireEvent.click(screen.getByRole("button", {name: "Settings"}));
    await screen.findByText("Configured");
    fireEvent.change(screen.getByLabelText("Provider credential"), {
      target: {value: "fixture-credential"}
    });
    fireEvent.click(screen.getByRole("button", {name: "Set key"}));
    await waitFor(() => {
      expect(client.setKeyringCredential).toHaveBeenCalledWith("fixture-credential");
    });
    fireEvent.click(screen.getByRole("button", {name: "Validate"}));
    fireEvent.click(screen.getByRole("button", {name: "Clear"}));
    expect(client.validateCredential).toHaveBeenCalledOnce();
    expect(client.clearKeyringCredential).toHaveBeenCalledOnce();
  });

  it("renders full approval decisions and structured input options", () => {
    const approval = event(1, "approval.required", {
      request_id: "approval",
      tool: "write_file",
      effect: "workspace write",
      allowed_scopes: ["once", "session"],
      replacement_allowed: true,
      edit_plan: {id: "a".repeat(64)}
    });
    const client = mockClient(snapshot([approval]));
    const approvalView = render(<App client={client} />);
    expect(screen.getByRole("button", {name: "Cancel"})).toBeTruthy();
    expect(screen.getByRole("button", {name: "Deny"})).toBeTruthy();
    const approve = screen.getByRole("button", {name: "Approve"});
    expect(screen.getByLabelText("Approval scope")).toBeTruthy();
    const replacement = screen.getByLabelText("Replacement arguments");
    fireEvent.change(replacement, {target: {value: "{"}});
    expect((approve as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(replacement, {target: {value: "{\"path\":\"safe\"}"}});
    fireEvent.change(screen.getByLabelText("Approval scope"), {
      target: {value: "session"}
    });
    fireEvent.click(approve);
    expect(client.decideApproval).toHaveBeenCalledWith(
      "approval",
      "approve",
      "a".repeat(64),
      "session",
      {path: "safe"}
    );
    approvalView.unmount();

    const inputClient = mockClient(snapshot([
      event(2, "input.required", {
        request_id: "input",
        prompt: "Choose",
        options: ["one", "two"]
      })
    ]));
    render(<App client={inputClient} />);
    fireEvent.change(screen.getByLabelText("Input options"), {
      target: {value: "two"}
    });
    fireEvent.click(screen.getByRole("button", {name: "Submit"}));
    expect(inputClient.replyInput).toHaveBeenCalledWith(
      "input",
      "two",
      {selection: "two"}
    );
  });

  it("resets approval submission state for a new request id", async () => {
    const firstClient = mockClient(snapshot([
      event(1, "approval.required", {
        request_id: "approval-1",
        tool: "write_file"
      })
    ]));
    vi.mocked(firstClient.decideApproval).mockImplementation(
      () => new Promise(() => {})
    );
    const view = render(<App client={firstClient} />);

    fireEvent.click(screen.getByRole("button", {name: "Approve"}));
    expect(screen.getByRole("button", {name: "Approve"}))
      .toHaveProperty("disabled", true);

    const secondClient = mockClient(snapshot([
      event(2, "approval.required", {
        request_id: "approval-2",
        tool: "exec_command"
      })
    ]));
    view.rerender(<App client={secondClient} />);

    await waitFor(() => {
      expect(screen.getByRole("button", {name: "Approve"}))
        .toHaveProperty("disabled", false);
    });
  });

  it("resets input submission state for a new request id", async () => {
    const firstClient = mockClient(snapshot([
      event(1, "input.required", {
        request_id: "input-1",
        prompt: "First input"
      })
    ]));
    vi.mocked(firstClient.replyInput).mockImplementation(
      () => new Promise(() => {})
    );
    const view = render(<App client={firstClient} />);
    const firstInput = screen.getByLabelText("Input answer");
    fireEvent.change(firstInput, {target: {value: "first"}});
    fireEvent.click(screen.getByRole("button", {name: "Submit"}));
    expect(screen.getByRole("button", {name: "Submit"}))
      .toHaveProperty("disabled", true);

    const secondClient = mockClient(snapshot([
      event(2, "input.required", {
        request_id: "input-2",
        prompt: "Second input"
      })
    ]));
    view.rerender(<App client={secondClient} />);

    await waitFor(() => {
      expect(screen.getByLabelText("Input answer")).toHaveProperty("value", "");
      expect(screen.getByRole("button", {name: "Submit"}))
        .toHaveProperty("disabled", true);
    });
    fireEvent.change(screen.getByLabelText("Input answer"), {
      target: {value: "second"}
    });
    expect(screen.getByRole("button", {name: "Submit"}))
      .toHaveProperty("disabled", false);
  });

  it("restores and persists the selected Session draft", async () => {
    const client = mockClient(snapshot());
    vi.mocked(client.loadDraft).mockResolvedValue("restored draft");
    render(<App client={client} />);

    const composer = await screen.findByDisplayValue("restored draft");
    fireEvent.change(composer, {target: {value: "edited draft"}});
    await waitFor(() => {
      expect(client.saveDraft).toHaveBeenCalledWith("edited draft", "session");
    });
  });

  it("disables Session-bound controls while the selected Session hydrates", () => {
    const value = snapshot([]);
    value.hydratingSessionID = value.selectedSessionID;
    render(<App client={mockClient(value)} />);

    expect(screen.getByText("Loading")).toBeTruthy();
    expect(screen.getByPlaceholderText("Ask CodeHelper"))
      .toHaveProperty("disabled", true);
    expect(screen.getByRole("button", {name: "Export session"}))
      .toHaveProperty("disabled", true);
  });

  it("opens external Markdown links safely and never loads model images", () => {
    const value = snapshot([
      event(1, "output.delta", {
        text: "[docs](https://example.com) ![remote](https://example.com/image.png)"
      })
    ]);
    const {container} = render(<App client={mockClient(value)} />);

    const link = screen.getByRole("link", {name: "docs"});
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("remote")).toBeTruthy();
  });

  it("windows 500-turn transcripts to 200 projected rows with older and newer navigation", () => {
    const events = Array.from({length: 500}, (_, index) => ({
      ...event(index + 1, "turn.completed", {
        text: `message-${index + 1}`,
        outcome: "answered"
      }),
      turn_id: `turn-${index + 1}`
    }));
    const {container} = render(<App client={mockClient(snapshot(events))} />);

    expect(container.querySelectorAll(".assistantMessage, .terminalState")).toHaveLength(200);
    expect(screen.queryByText("message-399")).toBeNull();
    expect(screen.getByText("message-500")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", {name: "Earlier messages"}));
    expect(screen.getByText("message-399")).toBeTruthy();
    expect(screen.getByRole("button", {name: "Newer messages"})).toBeTruthy();
  });
});

function snapshot(events: RuntimeEvent[] = []): RuntimeSnapshot {
  const session: SessionSummary = {
    version: 1,
    revision: 1,
    session_id: "session",
    thread_id: "thread",
    title: "Chat",
    status: "idle",
    pinned: false,
    archived: false,
    isolation: "shared",
    workspace_root: "/workspace",
    workspace_label: "workspace",
    latest_sequence: 0,
    pending_approvals: 0,
    pending_inputs: 0,
    checkpoint_count: 0,
    changed_files: 0,
    total_tokens: 0,
    cost_microunits: 0,
    cost_known: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z"
  };
  return {
    phase: "ready",
    workspaceRoot: "/workspace",
    includeArchived: false,
    contextResources: [],
    sessions: [session],
    selectedSessionID: session.session_id,
    hydratingSessionID: "",
    events,
    historyMoreBefore: false,
    providers: [
      {
        id: "fixture",
        display_name: "Fixture",
        selected: true,
        availability: "available"
      },
      {
        id: "offline",
        display_name: "Offline",
        selected: false,
        availability: "unavailable",
        reason: "Credential missing"
      }
    ],
    models: [
      {
        provider: "fixture",
        id: "fixture",
        selected: true,
        capabilities: modelCapabilities("Fixture")
      },
      {
        provider: "fixture",
        id: "reasoner",
        selected: false,
        capabilities: {
          ...modelCapabilities("Reasoner"),
          reasoning: true,
          reasoning_efforts: ["low", "high"]
        }
      }
    ],
    profile: {
      profile: {
        version: 1,
        revision: 1,
        mode: "act",
        provider: "fixture",
        model: "fixture",
        approval_posture: "suggest",
        execution_target: "local",
        max_steps: 32,
        enabled_tool_ids: ["read"]
      },
      capabilities: {
        provider: "fixture",
        model: "fixture",
        mutable_fields: [
          "mode", "provider", "model", "reasoning_effort",
          "approval_posture", "execution_target", "max_steps",
          "enabled_tool_ids"
        ],
        model_capabilities: {}
      }
    },
    tools: [{
      id: "read",
      name: "read_file",
      description: "Read a file",
      source_kind: "builtin",
      source_label: "CodeHelper",
      risk_level: "read",
      availability: "available",
      enabled: true,
      guarded: true
    }],
    checkpoints: [],
    tasks: [],
    agents: [],
    extensions: [],
    socketConnected: true
  };
}

function mockClient(value: RuntimeSnapshot): RuntimeClient {
  return {
    subscribe: () => () => {},
    getSnapshot: () => value,
    start: vi.fn(async () => {}),
    stop: vi.fn(),
    loadDraft: vi.fn(async () => ""),
    saveDraft: vi.fn(),
    decideApproval: vi.fn(async () => ({})),
    replyInput: vi.fn(async () => ({})),
    updateProfile: vi.fn(async () => {}),
    transitionPlan: vi.fn(async () => ({})),
    restoreCheckpoint: vi.fn(async () => ({})),
    forkCheckpoint: vi.fn(async () => ({})),
    setExtensionEnabled: vi.fn(async () => ({})),
    credentialStatus: vi.fn(async () => ({
      reference: {kind: "none", name: ""},
      configured: false,
      validation: "not_validated"
    })),
    setKeyringCredential: vi.fn(async () => ({})),
    validateCredential: vi.fn(async () => ({})),
    clearKeyringCredential: vi.fn(async () => ({})),
    browseWorkspace: vi.fn(async () => ({
      path: ".",
      entries: [],
      more: false
    })),
    readWorkspaceImage: vi.fn(async () => {
      throw new Error("image not configured");
    }),
    downloadWorkspaceContent: vi.fn(async () => new Blob()),
    workspaceDiff: vi.fn(async () => ({
      session_id: "session",
      thread_id: "thread",
      diff: "",
      digest: "0".repeat(64)
    })),
    searchWorkspaceSymbols: vi.fn(async () => ({
      query: "",
      status: "ready",
      symbols: []
    })),
    workspaceDiagnostics: vi.fn(async () => ({
      session_id: "session",
      thread_id: "thread",
      diagnostics: []
    })),
    addGitDiffContext: vi.fn(),
    addTerminalContext: vi.fn(async () => {}),
    addSymbolContext: vi.fn(),
    addDiagnosticsContext: vi.fn(),
    addImageContext: vi.fn()
  } as unknown as RuntimeClient;
}

function modelCapabilities(displayName: string) {
  return {
    display_name: displayName,
    context_window: 128_000,
    max_output_tokens: 8_192,
    streaming: true,
    reasoning: false,
    tool_calls: true,
    parallel_tool_calls: "unknown" as const,
    native_search: false,
    vision: false,
    image_input: false,
    prompt_cache: true,
    credential_status: "configured" as const,
    availability: "available" as const,
    selection_mode: "hot" as const
  };
}

function event(
  sequence: number,
  kind: string,
  data: Record<string, unknown>
): RuntimeEvent {
  return {
    version: 1,
    id: `event-${sequence}`,
    kind,
    operation_id: "operation",
    thread_id: "thread",
    turn_id: "turn",
    item_id: `item-${sequence}`,
    sequence,
    created_at: "2026-01-01T00:00:00Z",
    data
  };
}
