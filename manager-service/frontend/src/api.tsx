import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { State, Preferences } from "./types";
import { defaultPreferences } from "./types";
import {
  apiFetch,
  createLibraryClient,
  jsonResponse,
  type ApiRequest,
  type ApiQuery,
  type RequestOptions,
} from "./http-client";
import { createJobNotifications } from "./job-notifications";
import { createRefreshQueue } from "./refresh-queue";
import { useNotifications } from "./Notifications";

type AppActions = {
  refresh: () => Promise<void>;
  request: ApiRequest;
  query: ApiQuery;
  enqueue: (
    path: string,
    body?: unknown,
    options?: RequestOptions,
  ) => Promise<string>;
  notify: (text: string, error?: boolean) => void;
};
type AppSnapshot = { state: State | null; error: string };
type ImportPreferences = {
  preferences: Preferences;
  setPreferences: (p: Preferences) => void;
};
// Consumers subscribe to the smallest lifetime they need. Task progress must
// not invalidate command callbacks or every cover and library row.
const AppStateContext = createContext<AppSnapshot | null>(null);
const AppActionsContext = createContext<AppActions | null>(null);
const ImportPreferencesContext = createContext<ImportPreferences | null>(null);
const LibraryStateContext = createContext<{
  session: string;
  csrf: string;
  trackRevision: number;
  resourceRevision: number;
  musicPath: string;
  ready: boolean;
  scanning: boolean;
} | null>(null);

export function AppProvider({
  children,
  onSessionExpired,
}: {
  children: ReactNode;
  onSessionExpired: () => void;
}) {
  const [state, setState] = useState<State | null>(null),
    [error, setError] = useState("");
  const { begin, start, update, dismiss, notify } = useNotifications();
  const jobs = useMemo(
    () => createJobNotifications({ begin, start, update, dismiss }),
    [begin, start, update, dismiss],
  );
  const [preferences, setPreferencesState] = useState<Preferences>(() => {
    try {
      const saved = JSON.parse(
        localStorage.getItem("hifi-import-preferences") || "{}",
      );
      return {
        duplicate_policy: ["skip", "keep", "reject"].includes(
          saved?.duplicate_policy,
        )
          ? saved.duplicate_policy
          : "skip",
      };
    } catch {
      return defaultPreferences;
    }
  });
  const token = useRef(""),
    etag = useRef(""),
    storageScope = useRef("");
  const pollController = useRef<AbortController | null>(null);
  const session = state?.library_session || "";
  const { request, query } = useMemo(
    () =>
      createLibraryClient(
        session,
        () => storageScope.current,
        () => token.current,
      ),
    [session],
  );
  const refreshQueue = useMemo(
    () =>
      createRefreshQueue(async () => {
        const controller = pollController.current;
        if (!controller || controller.signal.aborted) return;
        try {
          const response = await apiFetch("state", undefined, token.current, {
            etag: etag.current,
            signal: controller.signal,
          });
          if (controller.signal.aborted) return;
          if (response.status === 401) {
            controller.abort();
            onSessionExpired();
            return;
          }
          if (response.status === 304) {
            setError("");
            jobs.reportCurrent();
            return;
          }
          const next = await jsonResponse<State>(response);
          if (controller.signal.aborted) return;
          const scope = next.library_session || "";
          if (storageScope.current !== scope) jobs.switchScope(next.jobs);
          storageScope.current = scope;
          etag.current = response.headers.get("ETag") || "";
          token.current = next.csrf;
          setError("");
          jobs.report(next.jobs);
          setState(next);
        } catch (e) {
          if (!controller.signal.aborted) {
            setError(e instanceof Error ? e.message : "连接失败");
          }
        }
      }),
    [jobs, onSessionExpired],
  );
  const { refresh, poll } = refreshQueue;
  const enqueue = useCallback(
    (path: string, body: unknown = {}, options?: RequestOptions) =>
      jobs.enqueue(path, body, request, refresh, options),
    [jobs, request, refresh],
  );
  const setPreferences = useCallback(
    (p: Preferences) => {
      setPreferencesState(p);
      try {
        localStorage.setItem("hifi-import-preferences", JSON.stringify(p));
      } catch {
        notify("设置已生效，但无法在当前浏览器中保存", true);
      }
    },
    [notify],
  );
  useEffect(() => {
    const controller = new AbortController();
    pollController.current = controller;
    void poll();
    const timer = window.setInterval(() => {
      if (!document.hidden) void poll();
    }, 1500);
    const visibility = () => {
      if (!document.hidden) void poll();
    };
    document.addEventListener("visibilitychange", visibility);
    return () => {
      controller.abort();
      clearInterval(timer);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, [poll]);
  const snapshot = useMemo(() => ({ state, error }), [state, error]);
  const actions = useMemo(
    () => ({ refresh, request, query, enqueue, notify }),
    [refresh, request, query, enqueue, notify],
  );
  const importPreferences = useMemo(
    () => ({ preferences, setPreferences }),
    [preferences, setPreferences],
  );
  const trackRevision = state?.cover_revision ?? 0;
  const resourceRevision = state?.resource_revision ?? 0;
  const musicPath = state?.music_path || "";
  const csrf = state?.csrf || "";
  const ready = !!state && state.storage?.ready !== false;
  const scanning = !!state?.jobs.some(
    (job) => job.kind === "scan" && ["queued", "running"].includes(job.state),
  );
  const library = useMemo(
    () => ({
      session,
      csrf,
      trackRevision,
      resourceRevision,
      musicPath,
      ready,
      scanning,
    }),
    [
      session,
      csrf,
      trackRevision,
      resourceRevision,
      musicPath,
      ready,
      scanning,
    ],
  );
  return (
    <AppActionsContext.Provider value={actions}>
      <ImportPreferencesContext.Provider value={importPreferences}>
        <LibraryStateContext.Provider value={library}>
          <AppStateContext.Provider value={snapshot}>
            {children}
          </AppStateContext.Provider>
        </LibraryStateContext.Provider>
      </ImportPreferencesContext.Provider>
    </AppActionsContext.Provider>
  );
}
function requiredContext<T>(value: T | null): T {
  if (value === null) throw new Error("AppProvider missing");
  return value;
}
export function useAppState() {
  return requiredContext(useContext(AppStateContext));
}
export function useAppActions() {
  return requiredContext(useContext(AppActionsContext));
}
export function useImportPreferences() {
  return requiredContext(useContext(ImportPreferencesContext));
}
export function useLibraryState() {
  return requiredContext(useContext(LibraryStateContext));
}
