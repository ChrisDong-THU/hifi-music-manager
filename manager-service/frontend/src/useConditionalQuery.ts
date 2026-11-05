import { useEffect, useMemo, useSyncExternalStore } from "react";
import { useAppActions } from "./api";
import { createConditionalResource } from "./conditional-resource";

export function useConditionalQuery<T>(
  path: string,
  revision: number | string = 0,
  enabled = true,
) {
  const { query } = useAppActions();
  const resource = useMemo(
    () =>
      createConditionalResource<T>((etag, signal) =>
        query<T>(path, { etag, signal }),
      ),
    [query, path],
  );
  const state = useSyncExternalStore(resource.subscribe, resource.getSnapshot);
  useEffect(() => {
    if (enabled) void resource.refresh();
    return resource.cancel;
  }, [resource, revision, enabled]);
  return {
    ...state,
    loading: enabled && (state.loading || (!state.data && !state.error)),
    refresh: resource.refresh,
  };
}
