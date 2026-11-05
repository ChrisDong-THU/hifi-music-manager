export type Page<T> = { items: T[]; total: number; revision: number };
export type PagedState<T> = Page<T> & {
  loaded: boolean;
  loading: boolean;
  error: string;
};

/** One owner for pagination, cancellation, retries and snapshot consistency. */
export function createPagedResource<T>(
  fetchPage: (page: number, signal: AbortSignal) => Promise<Page<T>>,
) {
  let state: PagedState<T> = {
    items: [],
    total: 0,
    revision: 0,
    loaded: false,
    loading: false,
    error: "",
  };
  let nextPage = 0;
  let epoch = 0;
  let active: { controller: AbortController; promise: Promise<void> } | null =
    null;
  const listeners = new Set<() => void>();
  const publish = (patch: Partial<PagedState<T>>) => {
    state = { ...state, ...patch };
    listeners.forEach((listener) => listener());
  };
  function cancel() {
    epoch++;
    active?.controller.abort();
    active = null;
    if (state.loading) publish({ loading: false });
  }
  function fetchNext(): Promise<void> {
    if (active) return active.promise;
    if (state.loaded && state.items.length >= state.total)
      return Promise.resolve();
    const controller = new AbortController();
    const page = nextPage;
    publish({ loading: true, error: "" });
    const promise = Promise.resolve()
      .then(() => fetchPage(page, controller.signal))
      .then((result) => {
        if (controller.signal.aborted) return;
        if (page > 0 && result.revision !== state.revision)
          throw new Error("曲库已变化，请刷新列表后重试");
        const items =
          page === 0 ? result.items : [...state.items, ...result.items];
        if (!result.items.length && items.length < result.total)
          throw new Error("分页数据不完整，请刷新后重试");
        nextPage = page + 1;
        publish({ ...result, items, loaded: true });
      })
      .catch((error) => {
        if (!controller.signal.aborted)
          publish({
            error: error instanceof Error ? error.message : "加载失败",
          });
      })
      .finally(() => {
        if (active?.controller === controller) {
          active = null;
          publish({ loading: false });
        }
      });
    active = { controller, promise };
    return promise;
  }
  const load = () =>
    active?.promise ?? (state.loaded ? Promise.resolve() : fetchNext());
  async function reload() {
    cancel();
    nextPage = 0;
    publish({ items: [], total: 0, loaded: false, error: "" });
    await fetchNext();
  }
  async function loadAll(): Promise<T[]> {
    const started = epoch;
    await load();
    for (;;) {
      if (started !== epoch) throw new DOMException("加载已取消", "AbortError");
      if (state.error) throw new Error(state.error);
      if (state.loaded && state.items.length >= state.total)
        return [...state.items];
      await fetchNext();
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
    load,
    loadMore: fetchNext,
    reload,
    loadAll,
    cancel,
  };
}
