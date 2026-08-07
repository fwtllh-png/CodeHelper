import assert from "node:assert/strict";
import test from "node:test";

import { decodeSessionToolCatalog } from "./tools.js";

const fixture = {
  version: 1,
  catalog_id: "catalog-1",
  generation: 2,
  digest: "digest-2",
  tools: [{
    id: "builtin:file_read",
    name: "file_read",
    description: "Read a file",
    source_kind: "builtin",
    source_label: "CodeHelper",
    capability: "read",
    access_mode: "read",
    risk_level: "low",
    sandbox_requirement: "none",
    policy_state: "deferred",
    policy_reason: "evaluated at call time",
    constitution_state: "deferred",
    constitution_reason: "enforced by Tool Guard",
    availability: "available",
    state: "eager",
    revision: 1,
    enabled: true,
    guarded: true,
  }],
};

void test("session tool catalog decoder preserves bounded state", () => {
  const decoded = decodeSessionToolCatalog(fixture);
  const tool = decoded.tools[0];
  assert.ok(tool);
  assert.equal(tool.id, "builtin:file_read");
  assert.equal(tool.guarded, true);
});

void test("session tool catalog decoder rejects forged fields and sources", () => {
  assert.throws(() => decodeSessionToolCatalog({
    ...fixture,
    tools: [{ ...fixture.tools[0], source_kind: "forged" }],
  }), /source kind/u);
  assert.throws(() => decodeSessionToolCatalog({
    ...fixture,
    tools: [{ ...fixture.tools[0], authority: 42 }],
  }), /unknown fields/u);
  assert.throws(() => decodeSessionToolCatalog({
    ...fixture,
    tools: [{ ...fixture.tools[0], guarded: false }],
  }), /not guarded/u);
  assert.throws(() => decodeSessionToolCatalog({
    ...fixture,
    tools: [{ ...fixture.tools[0], risk_level: "safe-enough" }],
  }), /risk level/u);
});
