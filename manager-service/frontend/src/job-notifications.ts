import type { ApiRequest, RequestOptions } from "./http-client.ts";
import type { Job } from "./types.ts";
import type { NoticeData } from "./notice-state.ts";
import { isInlineDraftJob, takeCompletedJobs } from "./jobs.ts";
import { taskLabels, completedLabels, taskKind } from "./task-labels.ts";

type NoticeInput = Omit<NoticeData, "id">;
type Sink = {
  begin: (label: string) => string;
  start: (id: string, input: NoticeInput) => void;
  update: (id: string, input: NoticeInput) => void;
  dismiss: (id: string) => void;
};

/** Owns task-to-notice identity, including polls that overtake POST responses. */
export function createJobNotifications(sink: Sink) {
  const notices = new Map<string, string>();
  const submissions = new Map<string, number>();
  const watched = new Set<string>();
  const reported = new Map<string, string>();
  let latest: Job[] = [];
  function switchScope(jobs: Job[]) {
    const retained = new Set(jobs.map((job) => job.id));
    for (const [jobID, noticeID] of notices) {
      if (retained.has(jobID)) continue;
      sink.dismiss(noticeID);
      notices.delete(jobID);
      watched.delete(jobID);
      reported.delete(jobID);
    }
    latest = jobs;
  }
  function report(jobs: Job[]) {
    latest = jobs;
    const visible = jobs.filter((job) => !isInlineDraftJob(job.kind));
    for (const job of visible) {
      if (!["queued", "running"].includes(job.state)) continue;
      let id = notices.get(job.id);
      const discovered = !id;
      if (!id) {
        if (submissions.get(job.kind)) continue;
        id = `job:${job.id}`;
        notices.set(job.id, id);
        watched.add(job.id);
      }
      const signature = JSON.stringify([
        job.state,
        job.message,
        job.done,
        job.total,
      ]);
      if (reported.get(job.id) === signature) continue;
      reported.set(job.id, signature);
      (discovered ? sink.start : sink.update)(id, {
        label: taskLabels[job.kind] || "处理中",
        status: "active",
        detail:
          job.state === "queued"
            ? "等待处理"
            : job.message === "等待处理"
              ? undefined
              : job.message,
        value: job.total > 0 ? (job.done / job.total) * 100 : undefined,
        count: job.total > 0 ? `${job.done}/${job.total}` : undefined,
      });
    }
    for (const job of takeCompletedJobs(visible, watched)) {
      sink.update(notices.get(job.id) || `job:${job.id}`, {
        label:
          job.state === "failed"
            ? job.message
            : completedLabels[job.kind] || "操作完成",
        status: job.state === "failed" ? "error" : "success",
        count:
          job.total > 0
            ? `${job.state === "failed" ? job.done : job.total}/${job.total}`
            : undefined,
      });
      notices.delete(job.id);
      reported.delete(job.id);
    }
  }
  async function enqueue(
    path: string,
    body: unknown,
    request: ApiRequest,
    refresh: () => Promise<void>,
    options?: RequestOptions,
  ) {
    const kind = taskKind(path);
    if (isInlineDraftJob(kind)) {
      try {
        return (await request<{ job: string }>(path, body, options)).job;
      } finally {
        await refresh();
      }
    }
    const id = sink.begin(taskLabels[kind] || "处理中");
    submissions.set(kind, (submissions.get(kind) || 0) + 1);
    try {
      const result = await request<{ job: string }>(path, body, options);
      watched.add(result.job);
      notices.set(result.job, id);
      await refresh();
      report(latest);
      return result.job;
    } catch (error) {
      sink.update(id, {
        label: error instanceof Error ? error.message : "操作失败",
        status: "error",
      });
      throw error;
    } finally {
      const remaining = (submissions.get(kind) || 1) - 1;
      if (remaining) submissions.set(kind, remaining);
      else submissions.delete(kind);
    }
  }
  return { report, reportCurrent: () => report(latest), switchScope, enqueue };
}
