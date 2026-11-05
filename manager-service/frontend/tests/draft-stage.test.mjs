import test from "node:test";
import assert from "node:assert/strict";
import { draftStage } from "../src/draft-stage.ts";
import { isInlineDraftJob } from "../src/jobs.ts";

const batch = {
  id: "album",
  files: [{ id: "file", hash: "audio" }],
  previewed: false,
};

test("entry labels follow independent read and detection phases without exposing results", () => {
  assert.equal(draftStage(batch, "preview", []).label, "等待读取");
  assert.equal(draftStage(batch, "quality", []).label, "等待检测");
  const reading = { ...batch, read_state: "running", quality_state: "queued" };
  assert.equal(draftStage(reading, "preview", []).label, "正在读取");
  const detecting = {
    ...batch,
    previewed: true,
    read_state: "done",
    quality_state: "running",
  };
  assert.equal(draftStage(detecting, "preview", []).label, "完成读取");
  assert.equal(draftStage(detecting, "quality", []).label, "正在检测");
  const complete = {
    ...detecting,
    quality_state: "done",
    quality: {
      file: { source_hash: "audio", upscaled: true, status: "analyzed" },
    },
  };
  assert.equal(draftStage(complete, "quality", []).label, "完成检测");
});

test("queued parent preparation updates child labels but unrelated tasks do not", () => {
  const child = {
    ...batch,
    parent_id: "parent",
    previewed: true,
    read_state: "done",
    quality_state: "done",
  };
  const job = {
    id: "job",
    batch_id: "parent",
    state: "queued",
    kind: "prepare",
  };
  assert.equal(draftStage(child, "preview", [job]).label, "等待读取");
  assert.equal(draftStage(child, "quality", [job]).busy, true);
  assert.equal(
    draftStage(child, "preview", [{ ...job, batch_id: "other" }]).label,
    "完成读取",
  );
});

test("failed and interrupted work remains retryable and never appears completed", () => {
  const failed = draftStage(
    { ...batch, quality_state: "failed", quality_error: "文件已变化" },
    "quality",
    [],
  );
  assert.equal(failed.label, "等待检测");
  assert.equal(failed.error, "文件已变化");
  assert.equal(
    draftStage({ ...batch, read_state: "interrupted" }, "preview", []).label,
    "等待读取",
  );
});

test("only draft processing uses inline status instead of task notifications", () => {
  for (const kind of ["prepare", "preview", "quality"])
    assert.equal(isInlineDraftJob(kind), true);
  for (const kind of ["scan", "import", "delete", "disks", "sync"])
    assert.equal(isInlineDraftJob(kind), false);
});
