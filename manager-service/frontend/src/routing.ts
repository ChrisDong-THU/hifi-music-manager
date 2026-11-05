import { useEffect, useState } from "react";

export function useRoute() {
  const [hash, setHash] = useState(location.hash);
  useEffect(() => {
    const update = () => setHash(location.hash);
    window.addEventListener("hashchange", update);
    return () => window.removeEventListener("hashchange", update);
  }, []);
  const [path, query] = (hash.slice(1) || "home").split("?");
  return { path, params: new URLSearchParams(query || "") };
}
export function navigate(path: string) {
  location.hash = path;
}
