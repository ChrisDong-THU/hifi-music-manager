import { useEffect, useState } from "react";
import { useAppActions } from "./api";
import type { LibraryStats } from "./types";

export function useScanStats(active: boolean) {
  const { request, refresh } = useAppActions();
  const [stats, setStats] = useState<LibraryStats | null>(null);
  useEffect(() => {
    if (!active) {
      setStats(null);
      return;
    }
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      let delay = 250;
      try {
        const progress = await request<{
          active: boolean;
          stats?: LibraryStats;
        }>("scan", undefined, { signal: controller.signal });
        if (controller.signal.aborted) return;
        if (progress.stats) setStats(progress.stats);
        if (!progress.active) {
          await refresh();
          return;
        }
      } catch {
        // The normal state poll reports connection errors. Retry this small
        // snapshot more slowly without adding duplicate error notifications.
        delay = 1000;
      }
      if (!controller.signal.aborted) timer = setTimeout(poll, delay);
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [active, request, refresh]);
  return active ? stats : null;
}
