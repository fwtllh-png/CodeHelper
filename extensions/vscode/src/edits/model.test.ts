import assert from "node:assert/strict";
import test from "node:test";

import {
  decodePlanDecisionTarget,
  decodePlanFileTarget,
  EditPlanProjector,
  projectEditPlan,
} from "./model.js";
import { decodeEvent } from "../protocol/decode.js";

void test("EditPlanProjector restores plans and joins path evidence", () => {
  const projector = new EditPlanProjector();
  projector.apply(event(1, "approval.required", approvalData()));
  projector.apply(event(2, "diagnostics.result", {
    tool: "file_apply",
    call_id: "call_1",
    receipts: [{
      path: "alpha.txt",
      status: "failed",
      message: "lint failed",
      diagnostics: [],
    }],
  }));
  projector.apply(event(3, "turn.verification", {
    status: "failed",
    mode: "repository",
    scope: "affected",
    action: "revert",
    repair_steps: 0,
    paths: ["nested/beta.txt"],
    checks: [{ name: "test", status: "failed" }],
  }));

  const review = projector.snapshot()[0];
  assert.ok(review);
  assert.equal(review.status, "pending");
  assert.equal(review.plan.files.length, 2);
  assert.deepEqual(review.annotations["alpha.txt"], [{
    kind: "diagnostics",
    status: "failed",
    detail: "lint failed",
  }]);
  assert.deepEqual(review.annotations["nested/beta.txt"], [{
    kind: "verification",
    status: "failed",
    detail: "test=failed",
  }]);

  projector.apply(event(4, "approval.resolved", {
    request_id: "approval_1",
    decision: "deny",
  }));
  assert.equal(projector.snapshot()[0]?.status, "deny");
});

void test("EditPlanProjector prefers pending plans then retains the latest", () => {
  const projector = new EditPlanProjector();
  projector.apply(event(1, "approval.required", approvalData()));
  projector.apply(event(2, "approval.resolved", {
    request_id: "approval_1",
    decision: "approve",
  }));
  projector.apply(event(3, "approval.required", {
    ...approvalData(),
    request_id: "approval_2",
    edit_plan: {
      ...approvalData().edit_plan,
      id: "c".repeat(64),
    },
  }));
  assert.deepEqual(
    projector.snapshot().map((review) => review.requestId),
    ["approval_2"],
  );
  projector.apply(event(4, "approval.resolved", {
    request_id: "approval_2",
    decision: "deny",
  }));
  assert.deepEqual(
    projector.snapshot().map((review) => review.requestId),
    ["approval_2"],
  );
});

void test("edit plan projection and tree command targets fail closed", () => {
  assert.throws(() => projectEditPlan({
    ...approvalData().edit_plan,
    files: [],
  }), /identity, diff, or file count/);
  assert.throws(() => projectEditPlan({
    ...approvalData().edit_plan,
    files: duplicateFirstFile(),
  }), /path or content/);
  assert.throws(() => projectEditPlan({
    ...approvalData().edit_plan,
    files: [{
      ...firstFile(),
      before_exists: true,
    }],
  }), /path or content/);
  assert.throws(() => projectEditPlan({
    ...approvalData().edit_plan,
    files: [{
      ...firstFile(),
      path: "../secret.txt",
    }],
  }), /path or content/);
  assert.deepEqual(decodePlanFileTarget({
    rootId: "a".repeat(64),
    planId: "b".repeat(64),
    fileIndex: 1,
  }), {
    rootId: "a".repeat(64),
    planId: "b".repeat(64),
    fileIndex: 1,
  });
  assert.throws(() => decodePlanFileTarget({
    rootId: "a".repeat(64),
    planId: "b".repeat(64),
    fileIndex: 1,
    path: "../../secret",
  }), /unknown fields/);
  assert.deepEqual(decodePlanDecisionTarget({
    rootId: "a".repeat(64),
    requestId: "approval_1",
    decision: "approve",
  }), {
    rootId: "a".repeat(64),
    requestId: "approval_1",
    decision: "approve",
  });
  assert.throws(() => decodePlanDecisionTarget({
    rootId: "a".repeat(64),
    requestId: "approval_1",
    decision: "always",
  }), /invalid/);
});

function duplicateFirstFile() {
  const first = firstFile();
  return [first, first];
}

function firstFile() {
  const first = approvalData().edit_plan.files[0];
  assert.ok(first);
  return first;
}

function approvalData() {
  return {
    request_id: "approval_1",
    call_id: "call_1",
    tool: "file_apply",
    arguments: {},
    arguments_digest: "a".repeat(64),
    resources: [
      { kind: "file", access: "write", path: "alpha.txt" },
      { kind: "file", access: "write", path: "nested/beta.txt" },
    ],
    allowed_scopes: ["once"],
    effect: "workspace.edit",
    risk: "high",
    reason_code: "edit_plan_required",
    expires_at: "2030-08-05T12:00:00Z",
    replacement_allowed: false,
    modifiable_arguments: [],
    edit_plan: {
      id: "b".repeat(64),
      diff: "--- /dev/null\n+++ b/alpha.txt\n",
      files: [{
        path: "alpha.txt",
        kind: "created",
        before_digest: "missing",
        after_digest: "c".repeat(64),
        before_exists: false,
        after_exists: true,
        after: "alpha\n",
      }, {
        path: "nested/beta.txt",
        kind: "created",
        before_digest: "missing",
        after_digest: "d".repeat(64),
        before_exists: false,
        after_exists: true,
        after: "beta\n",
      }],
    },
  };
}

function event(sequence: number, kind: string, data: unknown) {
  return decodeEvent({
    version: 1,
    id: `event_${String(sequence)}`,
    sequence,
    operation_id: "operation_1",
    thread_id: "thread_1",
    turn_id: "turn_1",
    item_id: "item_1",
    kind,
    created_at: "2026-08-05T00:00:00Z",
    data,
  });
}
