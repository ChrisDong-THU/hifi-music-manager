import test from "node:test";
import assert from "node:assert/strict";
import { draftOverall } from "../src/draft-overall.ts";

const batch = {
  id: "album",
  files: [
    { id: "one", hash: "a" },
    { id: "two", hash: "b" },
  ],
  read_state: "done",
  quality_state: "done",
  quality: {
    one: { status: "analyzed", source_hash: "a" },
    two: { status: "analyzed", source_hash: "b" },
  },
};

test("all songs need current completed analysis before the overall status can pass", () => {
  assert.equal(draftOverall(batch, []).state, "done");
  assert.equal(draftOverall({ ...batch, files: [] }, []).state, "empty");
  assert.equal(
    draftOverall({ ...batch, read_state: "queued" }, []).state,
    "waiting",
  );
  const stale = {
    ...batch,
    quality: {
      ...batch.quality,
      two: { status: "analyzed", source_hash: "old" },
    },
  };
  assert.equal(draftOverall(stale, []).state, "waiting");
  assert.match(draftOverall(stale, []).description, /待检测 1 首/);
});

test("the aggregate includes duplicates and quality issues, while unsupported results never pass", () => {
  const duplicate = draftOverall(
    { ...batch, duplicates: ["one", "one", "gone"] },
    [],
  );
  assert.equal(duplicate.state, "attention");
  assert.match(duplicate.description, /重复 1 首/);
  for (const flag of ["upscaled", "possible_upsampling"]) {
    const result = draftOverall(
      {
        ...batch,
        quality: {
          ...batch.quality,
          two: { ...batch.quality.two, [flag]: true },
        },
      },
      [],
    );
    assert.equal(result.state, "attention");
    assert.match(result.description, /音质需留意 1 首/);
  }
  for (const status of ["unsupported", "inconclusive", "error"]) {
    const result = draftOverall(
      {
        ...batch,
        quality: { ...batch.quality, two: { ...batch.quality.two, status } },
      },
      [],
    );
    assert.equal(result.state, status === "error" ? "failed" : "unverified");
  }
});

test("processing and retries replace the previous aggregate, including parent preparation", () => {
  const failed = {
    ...batch,
    quality_state: "failed",
    quality_error: "读取失败",
  };
  assert.equal(draftOverall(failed, []).state, "failed");
  const job = { kind: "quality", batch_id: "album", state: "running" };
  assert.equal(draftOverall(failed, [job]).state, "running");
  assert.equal(
    draftOverall(batch, [{ ...job, state: "queued" }]).state,
    "waiting",
  );
  assert.equal(
    draftOverall(batch, [{ ...job, batch_id: "other" }]).state,
    "done",
  );
  assert.equal(
    draftOverall({ ...batch, parent_id: "parent" }, [
      { ...job, kind: "prepare", batch_id: "parent" },
    ]).state,
    "running",
  );
});
