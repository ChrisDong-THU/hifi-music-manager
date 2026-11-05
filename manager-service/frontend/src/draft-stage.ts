import type { Draft, Job } from "./types";
import { currentQuality } from "./quality-result.ts";

export type DraftStageKind = "preview" | "quality";

export function relatedDraftJob(batch: Draft, job: Job) {
  return (
    ["queued", "running"].includes(job.state) &&
    (job.batch_id === batch.id ||
      (!!batch.parent_id && job.batch_id === batch.parent_id))
  );
}

export function draftStage(batch: Draft, kind: DraftStageKind, jobs: Job[]) {
  const operation = kind === "preview" ? "读取标签" : "检测音源";
  const shortOperation = kind === "preview" ? "读取" : "检测";
  const completedQuality =
    batch.files.length > 0 &&
    batch.files.every(
      (track) => !!currentQuality(track, batch.quality?.[track.id]),
    );
  let state =
    kind === "preview"
      ? batch.read_state || (batch.previewed ? "done" : "queued")
      : batch.quality_state || (completedQuality ? "done" : "queued");
  const error =
    kind === "preview" ? batch.read_error || "" : batch.quality_error || "";
  const active = jobs.filter((job) => relatedDraftJob(batch, job));
  for (const job of active) {
    if (job.kind !== kind && job.kind !== "prepare") continue;
    if (job.state === "queued") state = "queued";
    else if (job.kind === kind) state = "running";
  }
  const failed = state === "failed" || state === "interrupted";
  const phase: "queued" | "running" | "done" =
    state === "running" ? "running" : state === "done" ? "done" : "queued";
  return {
    phase,
    operation,
    shortOperation,
    label: `${phase === "running" ? "正在" : phase === "done" ? "完成" : "等待"}${shortOperation}`,
    error: failed ? error || "处理未完成，点击标签重试" : "",
    busy: active.length > 0,
  };
}
