import { useEffect, useMemo, useSyncExternalStore } from "react";
import { useAppActions } from "./api";
import { createPagedResource } from "./paged-resource";
import type { Album, Track } from "./types";

type Items = { tracks: Track; albums: Album };

export function useLibraryPage<K extends keyof Items>(
  kind: K,
  path: string,
  revision: number,
  enabled = true,
) {
  const { request } = useAppActions();
  const resource = useMemo(
    () =>
      createPagedResource<Items[K]>(async (page, signal) => {
        const result = await request<
          { total: number; revision: number } & { [P in K]: Items[K][] }
        >(`${path}${path.includes("?") ? "&" : "?"}page=${page}`, undefined, {
          signal,
        });
        return {
          items: result[kind],
          total: result.total,
          revision: result.revision,
        };
      }),
    [request, kind, path, revision],
  );
  const state = useSyncExternalStore(resource.subscribe, resource.getSnapshot);
  useEffect(() => {
    if (enabled) void resource.load();
    else resource.cancel();
    return resource.cancel;
  }, [resource, enabled]);
  return {
    ...state,
    loading: enabled && (state.loading || (!state.loaded && !state.error)),
    loadMore: resource.loadMore,
    reload: resource.reload,
    loadAll: resource.loadAll,
  };
}
