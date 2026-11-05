import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useMediaQuery } from "@mui/material";
import type { LibraryStats } from "./types";
import { createStatsAnimation, emptyStats } from "./stats-animation";

export function useAnimatedStats(target: LibraryStats | null, cycle: number) {
  const initial = useRef(target || emptyStats);
  const [value, setValue] = useState(initial.current);
  const reduced = useMediaQuery("(prefers-reduced-motion: reduce)");
  const animation = useMemo(
    () => createStatsAnimation(setValue, initial.current),
    [],
  );
  const previousCycle = useRef(cycle);
  useLayoutEffect(() => {
    if (previousCycle.current === cycle) return;
    previousCycle.current = cycle;
    animation.reset();
  }, [animation, cycle]);
  useEffect(() => {
    if (target) animation.to(target, reduced ? 0 : 1000);
  }, [animation, target, reduced]);
  useEffect(() => () => animation.stop(), [animation]);
  return value;
}
