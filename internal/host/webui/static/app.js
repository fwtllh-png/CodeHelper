(() => {
  const logEl = document.getElementById("log");
  const promptEl = document.getElementById("prompt");
  const createBtn = document.getElementById("create-thread");
  const startBtn = document.getElementById("start-turn");
  const cancelBtn = document.getElementById("cancel-turn");
  const approveBtn = document.getElementById("approve");

  let threadId = "";
  let turnId = "";
  let approvalRequestId = "";
  let eventSource = null;

  function log(message) {
    logEl.textContent += message + "\n";
    logEl.scrollTop = logEl.scrollHeight;
  }

  async function createThread() {
    const response = await fetch("/v1/threads", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({}),
    });
    const body = await response.json();
    if (!response.ok) {
      log("create thread failed: " + JSON.stringify(body));
      return;
    }
    threadId = body.thread_id || body.id || body.thread?.id || "";
    startBtn.disabled = !threadId;
    cancelBtn.disabled = true;
    approveBtn.disabled = true;
    log("thread=" + threadId);
    if (eventSource) eventSource.close();
    eventSource = new EventSource("/v1/events");
    eventSource.onmessage = (event) => {
      log("event: " + event.data);
      try {
        const payload = JSON.parse(event.data);
        if (payload.type === "approval.requested" || payload.approval_request_id) {
          approvalRequestId = payload.approval_request_id || payload.request_id || "";
          approveBtn.disabled = !approvalRequestId;
        }
      } catch (_) {}
    };
  }

  async function startTurn() {
    if (!threadId) return;
    const response = await fetch("/v1/threads/" + encodeURIComponent(threadId) + "/turns", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ input: promptEl.value || "hello" }),
    });
    const body = await response.json();
    if (!response.ok) {
      log("start turn failed: " + JSON.stringify(body));
      return;
    }
    turnId = body.turn_id || body.id || body.turn?.id || "";
    cancelBtn.disabled = !turnId;
    log("turn=" + turnId);
  }

  async function cancelTurn() {
    if (!threadId || !turnId) return;
    const response = await fetch(
      "/v1/threads/" + encodeURIComponent(threadId) + "/turns/" + encodeURIComponent(turnId) + "/cancel",
      { method: "POST", headers: { "content-type": "application/json" }, body: "{}" },
    );
    log("cancel status=" + response.status);
  }

  async function approve() {
    if (!threadId || !turnId || !approvalRequestId) return;
    const response = await fetch(
      "/v1/threads/" +
        encodeURIComponent(threadId) +
        "/turns/" +
        encodeURIComponent(turnId) +
        "/approvals/" +
        encodeURIComponent(approvalRequestId) +
        "/decision",
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ decision: "allow" }),
      },
    );
    log("approve status=" + response.status);
  }

  createBtn.addEventListener("click", () => createThread().catch((err) => log(String(err))));
  startBtn.addEventListener("click", () => startTurn().catch((err) => log(String(err))));
  cancelBtn.addEventListener("click", () => cancelTurn().catch((err) => log(String(err))));
  approveBtn.addEventListener("click", () => approve().catch((err) => log(String(err))));
  log("codehelper web control ready");
})();
