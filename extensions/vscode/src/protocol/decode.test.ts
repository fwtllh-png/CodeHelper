import assert from "node:assert/strict";
import test from "node:test";

import { decodeEvent, isUnknownEvent } from "./decode.js";

const eventReferences = {
  sequence: 1,
  operation_id: "op_1",
  thread_id: "thr_1",
  turn_id: "turn_1",
  item_id: "",
};

void test("decodeEvent accepts a known event", () => {
  const event = decodeEvent({
    version: 1,
    id: "evt_known",
    ...eventReferences,
    kind: "output.delta",
    created_at: "2026-08-04T00:00:00Z",
    data: { text: "hello" },
  });

  assert.equal(event.kind, "output.delta");
  assert.equal(isUnknownEvent(event), false);
});

void test("decodeEvent preserves an unknown event for generic display", () => {
  const input = {
    version: 1,
    id: "evt_future",
    ...eventReferences,
    kind: "future.capability",
    created_at: "2026-08-04T00:00:00Z",
    data: { safe: true },
  };
  const event = decodeEvent(input);

  assert.equal(isUnknownEvent(event), true);
  if (isUnknownEvent(event)) {
    assert.equal(event.kind, "future.capability");
    assert.deepEqual(event.raw, input);
  }
});

void test("decodeEvent rejects malformed envelopes", () => {
  assert.throws(
    () => decodeEvent({
      version: 1,
      id: "",
      ...eventReferences,
      kind: "output.delta",
      created_at: "2026-08-04T00:00:00Z",
      data: {},
    }),
    /event\.id/,
  );
  assert.throws(() => decodeEvent(null), /event must be an object/);
  assert.throws(
    () => decodeEvent({
      version: 1,
      id: "evt_null",
      ...eventReferences,
      kind: "future.capability",
      created_at: "2026-08-04T00:00:00Z",
      data: null,
    }),
    /event\.data must be present and non-null/u,
  );
});

void test("decodeEvent rejects version skew before known-kind projection", () => {
  assert.throws(
    () => decodeEvent({
      version: 2,
      id: "evt_future_version",
      ...eventReferences,
      kind: "output.delta",
      created_at: "2026-08-04T00:00:00Z",
      data: { text: "must not be cast as current protocol" },
    }),
    /event\.version 2 is unsupported/u,
  );
});
