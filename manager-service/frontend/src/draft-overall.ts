import type { Draft, Job } from "./types";
import { draftStage, relatedDraftJob } from "./draft-stage.ts";
import { currentQuality } from "./quality-result.ts";

export type DraftOverallState =
  | "empty"
  | "waiting"
  | "running"
  | "failed"
  | "attention"
  | "unverified"
  | "done";

const labels: Record<DraftOverallState, string> = {
  empty: "暂无歌曲",
  waiting: "等待处理",
  running: "正在处理",
  failed: "处理失败",
  attention: "存在需留意的歌曲",
  unverified: "部分音源无法判断",
  done: "所有歌曲未发现异常",
};

export function draftOverall(batch: Draft, jobs: Job[]) {
  const read = draftStage(batch, "preview", jobs);
  const quality = draftStage(batch, "quality", jobs);
  const duplicateIDs = new Set(batch.duplicates);
  let duplicates = 0,
    warnings = 0,
    failures = 0,
    unverified = 0,
    pending = 0;
  for (const file of batch.files) {
    if (duplicateIDs.has(file.id)) duplicates++;
    const result = currentQuality(file, batch.quality?.[file.id]);
    if (!result) pending++;
    else if (result.status === "error") failures++;
    else {
      if (result.upscaled || result.possible_upsampling) warnings++;
      if (result.status !== "analyzed") unverified++;
    }
  }
  const running =
    read.phase === "running" ||
    quality.phase === "running" ||
    jobs.some(
      (job) =>
        relatedDraftJob(batch, job) &&
        job.state === "running" &&
        ["prepare", "preview", "quality"].includes(job.kind),
    );
  const stageError = read.error || quality.error;
  let state: DraftOverallState = "done";
  if (!batch.files.length) state = "empty";
  else if (running) state = "running";
  else if (stageError || failures) state = "failed";
  else if (read.phase !== "done" || quality.phase !== "done" || pending)
    state = "waiting";
  else if (duplicates || warnings) state = "attention";
  else if (unverified) state = "unverified";
  const details = [
    duplicates ? `重复 ${duplicates} 首` : "",
    warnings ? `音质需留意 ${warnings} 首` : "",
    failures ? `检测失败 ${failures} 首` : "",
    unverified ? `音源无法判断 ${unverified} 首` : "",
    pending ? `待检测 ${pending} 首` : "",
    read.error ? "读取失败" : "",
    quality.error && !failures ? "检测失败" : "",
  ].filter(Boolean);
  return {
    state,
    label: labels[state],
    description: details.join("；") || labels[state],
  };
}
