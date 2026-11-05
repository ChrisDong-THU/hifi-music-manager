import test from "node:test";
import assert from "node:assert/strict";
import { takeCompletedJobs, hasActiveFileJobs } from "../src/jobs.ts";

test("playback retries do not lock editing, while queued file work and disk changes do", () => {
  assert.equal(hasActiveFileJobs([{ kind: "sync", state: "running" }]), false);
  for (const kind of [
    "scan",
    "edit",
    "preview",
    "prepare",
    "quality",
    "import",
    "delete",
    "draft-delete",
    "disks",
  ]) {
    assert.equal(hasActiveFileJobs([{ kind, state: "queued" }]), true, kind);
    assert.equal(hasActiveFileJobs([{ kind, state: "running" }]), true, kind);
    assert.equal(hasActiveFileJobs([{ kind, state: "done" }]), false, kind);
    assert.equal(hasActiveFileJobs([{ kind, state: "failed" }]), false, kind);
  }
  assert.equal(
    hasActiveFileJobs([
      { kind: "sync", state: "running" },
      { kind: "edit", state: "running" },
    ]),
    true,
  );
});

test("a fast job discovered before registration is reported once even after unchanged polls", () => {
  const cached = [{ id: "fast", state: "done", kind: "edit" }];
  const watched = new Set();
  assert.deepEqual(takeCompletedJobs(cached, watched), []);
  watched.add("fast");
  assert.deepEqual(takeCompletedJobs(cached, watched), cached);
  assert.deepEqual(takeCompletedJobs(cached, watched), []);
});

test("pending and unrelated jobs are retained; failures are consumed once", () => {
  const watched = new Set(["pending", "failed"]);
  const failed = { id: "failed", state: "failed", message: "磁盘已满" };
  assert.deepEqual(
    takeCompletedJobs(
      [
        { id: "pending", state: "running" },
        { id: "background", state: "done" },
        failed,
      ],
      watched,
    ),
    [failed],
  );
  assert.deepEqual([...watched], ["pending"]);
});
