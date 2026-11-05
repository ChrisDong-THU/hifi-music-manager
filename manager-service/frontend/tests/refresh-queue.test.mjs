import assert from "node:assert/strict";
import test from "node:test";
import { createRefreshQueue } from "../src/refresh-queue.ts";

function setup() {
  const runs = [];
  const queue = createRefreshQueue(
    () =>
      new Promise((resolve, reject) => {
        runs.push({ resolve, reject });
      }),
  );
  return { ...queue, runs };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));

test("polls share a request, while post-mutation refresh waits for a fresh response", async () => {
  const q = setup();
  const old = q.poll();
  assert.equal(q.poll(), old);
  await tick();
  const fresh = q.refresh();
  assert.equal(q.refresh(), fresh);
  assert.notEqual(fresh, old);
  let finished = false;
  fresh.then(() => {
    finished = true;
  });
  q.runs[0].resolve();
  await old;
  await tick();
  assert.equal(q.runs.length, 2);
  assert.equal(finished, false);
  q.runs[1].resolve();
  await fresh;
  assert.equal(finished, true);
});

test("a mutation during the follow-up request gets another fresh request", async () => {
  const q = setup();
  const initial = q.poll();
  const firstMutation = q.refresh();
  await tick();
  q.runs[0].resolve();
  await initial;
  await tick();
  const secondMutation = q.refresh();
  q.runs[1].resolve();
  await firstMutation;
  await tick();
  assert.equal(q.runs.length, 3);
  q.runs[2].resolve();
  await secondMutation;
});

test("a failed poll cannot poison queued refreshes or later polls", async () => {
  const q = setup();
  const failed = q.poll();
  const rejection = assert.rejects(failed, /offline/);
  const fresh = q.refresh();
  await tick();
  q.runs[0].reject(new Error("offline"));
  await rejection;
  await tick();
  q.runs[1].resolve();
  await fresh;
  const later = q.poll();
  await tick();
  assert.equal(q.runs.length, 3);
  q.runs[2].resolve();
  await later;
});
