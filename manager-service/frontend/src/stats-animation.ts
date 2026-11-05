import type { LibraryStats } from "./types.ts";

export const emptyStats: LibraryStats = {
  tracks: 0,
  artists: 0,
  albums: 0,
  bytes: 0,
  duration: 0,
};

type Clock = {
  now: () => number;
  request: (callback: (time: number) => void) => number;
  cancel: (id: number) => void;
};

export function createStatsAnimation(
  publish: (value: LibraryStats) => void,
  initial: LibraryStats = emptyStats,
  clock: Clock = {
    now: () => performance.now(),
    request: (callback) => requestAnimationFrame(callback),
    cancel: (id) => cancelAnimationFrame(id),
  },
) {
  let value = initial;
  let goal: LibraryStats | null = null;
  let frame: number | undefined;
  const keys = Object.keys(emptyStats) as (keyof LibraryStats)[];
  function stop() {
    if (frame !== undefined) clock.cancel(frame);
    frame = undefined;
    goal = null;
  }
  return {
    stop,
    reset() {
      stop();
      goal = null;
      value = { ...emptyStats };
      publish(value);
    },
    to(target: LibraryStats, duration = 1000) {
      if (
        duration > 0 &&
        goal &&
        keys.every((key) => goal![key] === target[key])
      )
        return;
      stop();
      goal = { ...target };
      const destination = goal;
      if (duration <= 0 || keys.every((key) => value[key] === target[key])) {
        value = destination;
        publish(value);
        return;
      }
      const from = value;
      const started = clock.now();
      function tick(time: number) {
        const progress = Math.min(1, Math.max(0, (time - started) / duration));
        const eased = 1 - (1 - progress) ** 3;
        value =
          progress === 1
            ? destination
            : (Object.fromEntries(
                keys.map((key) => [
                  key,
                  from[key] + (destination[key] - from[key]) * eased,
                ]),
              ) as LibraryStats);
        frame = undefined;
        publish(value);
        if (progress < 1) frame = clock.request(tick);
      }
      frame = clock.request(tick);
    },
  };
}
