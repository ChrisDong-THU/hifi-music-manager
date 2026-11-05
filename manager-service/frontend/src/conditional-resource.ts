import type { QueryResult } from "./http-client.ts";

// A feature owns its read snapshot and ETag. Control-plane polls provide only
// invalidation hints; they never embed another feature's complete entities.
export function createConditionalResource<T>(
  read: (etag: string, signal: AbortSignal) => Promise<QueryResult<T>>,
) {
  let state: { data: T | null; loading: boolean; error: string } = {
    data: null,
    loading: false,
    error: "",
  };
  let etag = "";
  let active: AbortController | null = null;
  const listeners = new Set<() => void>();
  const publish = (patch: Partial<typeof state>) => {
    state = { ...state, ...patch };
    listeners.forEach((fn) => fn());
  };
  function cancel() {
    active?.abort();
    active = null;
    if (state.loading) publish({ loading: false });
  }
  async function refresh() {
    cancel();
    const controller = new AbortController();
    active = controller;
    publish({ loading: true, error: "" });
    try {
      const response = await read(etag, controller.signal);
      if (controller.signal.aborted) return;
      if (response.notModified) {
        if (state.data === null) throw new Error("缺少查询快照，请重试");
      } else {
        etag = response.etag;
        publish({ data: response.data });
      }
    } catch (error) {
      if (!controller.signal.aborted)
        publish({ error: error instanceof Error ? error.message : "加载失败" });
    } finally {
      if (active === controller) {
        active = null;
        publish({ loading: false });
      }
    }
  }
  return {
    getSnapshot: () => state,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    refresh,
    cancel,
  };
}
