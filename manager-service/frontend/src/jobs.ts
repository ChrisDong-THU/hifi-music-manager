import type { Job } from "./types";

export const isInlineDraftJob = (kind: string) =>
  ["prepare", "preview", "quality"].includes(kind);

export function hasActiveFileJobs(jobs: Job[]) {
  return jobs.some((job) => ["queued", "running"].includes(job.state));
}

// Re-check cached jobs after registering a mutation: its completion may have
// reached a concurrent poll before the mutation's response reached the browser.
export function takeCompletedJobs(jobs: Job[], watched: Set<string>) {
  return jobs.filter(
    (job) => ["done", "failed"].includes(job.state) && watched.delete(job.id),
  );
}
