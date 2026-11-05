import test from "node:test";
import assert from "node:assert/strict";
import { createConditionalResource } from "../src/conditional-resource.ts";

test("a feature owns its ETag and preserves data identity on 304", async () => {
  const tags = [];
  const data = { drafts: [{ id: "one" }] };
  const resource = createConditionalResource(async (etag) => {
    tags.push(etag);
    return etag
      ? { notModified: true }
      : { notModified: false, data, etag: '"one"' };
  });
  await resource.refresh();
  await resource.refresh();
  assert.deepEqual(tags, ["", '"one"']);
  assert.equal(resource.getSnapshot().data, data);
});

test("late workspace responses cannot overwrite a newer query snapshot", async () => {
  const pending = [];
  const resource = createConditionalResource(
    (_etag, signal) =>
      new Promise((resolve) => pending.push({ signal, resolve })),
  );
  const first = resource.refresh();
  const second = resource.refresh();
  assert.equal(pending[0].signal.aborted, true);
  pending[1].resolve({ notModified: false, data: "new", etag: "new" });
  await second;
  pending[0].resolve({ notModified: false, data: "old", etag: "old" });
  await first;
  assert.equal(resource.getSnapshot().data, "new");
});

test("a feature error retains its last snapshot and can recover independently", async () => {
  let fail = false;
  const resource = createConditionalResource(async () => {
    if (fail) throw new Error("workspace unavailable");
    return { notModified: false, data: ["valid"], etag: "saved" };
  });
  await resource.refresh();
  fail = true;
  await resource.refresh();
  assert.deepEqual(resource.getSnapshot().data, ["valid"]);
  assert.equal(resource.getSnapshot().error, "workspace unavailable");
  fail = false;
  await resource.refresh();
  assert.equal(resource.getSnapshot().error, "");
});

test("304 without a previously loaded resource is a visible protocol error", async () => {
  const resource = createConditionalResource(async () => ({
    notModified: true,
  }));
  await resource.refresh();
  assert.match(resource.getSnapshot().error, /缺少查询快照/);
});
