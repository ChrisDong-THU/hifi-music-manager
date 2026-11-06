import { useMemo, useSyncExternalStore } from "react";

function parseRoute(hash: string) {
  const [path, query] = (hash.slice(1) || "home").split("?");
  return { path, params: new URLSearchParams(query || "") };
}

function subscribe(listener: () => void) {
  window.addEventListener("hashchange", listener);
  return () => window.removeEventListener("hashchange", listener);
}

const getHash = () => location.hash;

export function useRoute() {
  const hash = useSyncExternalStore(subscribe, getHash);
  return useMemo(() => parseRoute(hash), [hash]);
}

export function navigate(path: string) {
  location.hash = path;
}
