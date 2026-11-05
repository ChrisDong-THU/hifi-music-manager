import test from "node:test";
import assert from "node:assert/strict";
import { createPagedResource } from "../src/paged-resource.ts";

test("concurrent consumers share a page request and full playback reuses loaded pages", async () => {
  let resolve;
  const pages = [];
  const resource = createPagedResource(async (page) => {
    pages.push(page);
    if (page === 0)
      return new Promise((done) => {
        resolve = done;
      });
    return { items: ["second"], total: 2, revision: 7 };
  });
  const first = resource.load();
  assert.equal(resource.loadMore(), first);
  await Promise.resolve();
  resolve({ items: ["first"], total: 2, revision: 7 });
  await first;
  assert.deepEqual(await resource.loadAll(), ["first", "second"]);
  assert.deepEqual(await resource.loadAll(), ["first", "second"]);
  assert.deepEqual(pages, [0, 1]);
});

test("a changed library snapshot never becomes a mixed pagination result", async () => {
  let revision = 1;
  const resource = createPagedResource(async (page) => ({
    items: [page],
    total: 2,
    revision: revision++,
  }));
  await assert.rejects(resource.loadAll(), /曲库已变化/);
  assert.deepEqual(resource.getSnapshot().items, [0]);
  await resource.reload();
  assert.equal(resource.getSnapshot().revision, 3);
  assert.equal(resource.getSnapshot().error, "");
});

test("a late response after cancellation cannot publish into a new view", async () => {
  let resolve, signal;
  const resource = createPagedResource((_page, received) => {
    signal = received;
    return new Promise((done) => {
      resolve = done;
    });
  });
  const pending = resource.loadAll();
  await Promise.resolve();
  resource.cancel();
  assert.equal(signal.aborted, true);
  resolve({ items: ["old"], total: 1, revision: 1 });
  await assert.rejects(pending, { name: "AbortError" });
  assert.equal(resource.getSnapshot().loaded, false);
  assert.deepEqual(resource.getSnapshot().items, []);
});

test("failed pagination retries the same page while preserving existing results", async () => {
  const calls = [];
  let fail = true;
  const resource = createPagedResource(async (page) => {
    calls.push(page);
    if (page === 1 && fail) throw new Error("offline");
    return { items: [page], total: 2, revision: 4 };
  });
  await resource.load();
  await resource.loadMore();
  assert.equal(resource.getSnapshot().error, "offline");
  assert.deepEqual(resource.getSnapshot().items, [0]);
  fail = false;
  await resource.loadMore();
  assert.deepEqual(resource.getSnapshot().items, [0, 1]);
  assert.deepEqual(calls, [0, 1, 1]);
});

test("an incomplete empty page cannot leave full-album playback in a fetch loop", async () => {
  const resource = createPagedResource(async () => ({
    items: [],
    total: 1,
    revision: 1,
  }));
  await assert.rejects(resource.loadAll(), /分页数据不完整/);
  assert.equal(resource.getSnapshot().loading, false);
});
