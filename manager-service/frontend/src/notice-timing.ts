export const noticeTimings = {
  loadingDelay: 300,
  minimumVisible: 1500,
  success: 3500,
  settle: 520,
  enter: 320,
  exit: 220,
  collapse: 160,
} as const;

type Clock = {
  now: () => number;
  schedule: (
    callback: () => void,
    delay: number,
  ) => ReturnType<typeof setTimeout>;
  cancel: (timer: ReturnType<typeof setTimeout>) => void;
};

// Count reading time only while the message is entered, visible and unattended.
// A revision changes only when readable terminal content changes, not on polling.
export function createNoticeTimer(
  expire: (revision: string) => void,
  clock: Clock = {
    now: () => performance.now(),
    schedule: (callback, delay) => setTimeout(callback, delay),
    cancel: (timer) => clearTimeout(timer),
  },
) {
  const pauses = new Set<string>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let startedAt = 0;
  let remaining: number | null = null;
  let revision: string | undefined;
  let duration: number | null = null;
  let epoch = 0;
  function stop() {
    epoch++;
    if (timer === undefined) return;
    clock.cancel(timer);
    timer = undefined;
    if (remaining !== null)
      remaining = Math.max(0, remaining - (clock.now() - startedAt));
  }
  function resume() {
    if (
      timer !== undefined ||
      remaining === null ||
      pauses.size ||
      revision === undefined
    )
      return;
    const scheduledEpoch = ++epoch;
    const scheduledRevision = revision;
    startedAt = clock.now();
    timer = clock.schedule(() => {
      if (scheduledEpoch !== epoch) return;
      timer = undefined;
      remaining = null;
      expire(scheduledRevision);
    }, remaining);
  }
  return {
    configure(nextRevision: string, nextDuration: number | null) {
      if (revision === nextRevision && duration === nextDuration) return;
      stop();
      revision = nextRevision;
      duration = nextDuration;
      remaining = nextDuration;
      resume();
    },
    pause(reason: string, paused: boolean) {
      if (paused) {
        if (pauses.has(reason)) return;
        stop();
        pauses.add(reason);
      } else {
        pauses.delete(reason);
        resume();
      }
    },
    dispose() {
      stop();
      remaining = null;
      revision = undefined;
    },
  };
}
