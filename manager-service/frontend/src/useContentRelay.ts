import { useLayoutEffect, useRef } from "react";

const beatDuration = 215;
const beatInterval = 30;
const relayEase = "cubic-bezier(0.22, 0.72, 0.25, 1)";

/** Animate marked regions in order after a view changes, without delaying its render. */
export function useContentRelay(
  view: string | null,
  { onMount = true, direction = 1 }: { onMount?: boolean; direction?: 1 | -1 } = {},
) {
  const region = useRef<HTMLDivElement>(null);
  const previous = useRef<string | null>(null);

  useLayoutEffect(() => {
    const from = previous.current;
    previous.current = view;
    const node = region.current;
    if (!node?.animate || view === null || (!onMount && (from === null || from === view))) return;

    const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
    if (reducedMotion.matches || document.hidden) return;

    const beats = [...node.querySelectorAll<HTMLElement>("[data-relay-beat]")]
      .filter((element) => element.getClientRects().length > 0);
    const stages = [...new Set(beats.map((element) => Number(element.dataset.relayBeat)))];
    stages.sort((a, b) => a - b);
    const animations = new Map<HTMLElement, Animation>();
    for (const element of beats) {
      if (element.contains(document.activeElement)) continue;
      const stage = stages.indexOf(Number(element.dataset.relayBeat));
      const animation = element.animate(
        [
          { opacity: 0, translate: `${direction * 12}px 0` },
          { opacity: 1, translate: "0 0" },
        ],
        {
          id: "content-relay",
          duration: beatDuration,
          delay: stage * beatInterval,
          easing: relayEase,
          fill: "backwards",
        },
      );
      animations.set(element, animation);
    }
    if (!animations.size) return;

    // If a user tabs into a waiting beat, reveal the focused control immediately.
    const revealFocused = (event: FocusEvent) => {
      if (!(event.target instanceof Node)) return;
      for (const [element, animation] of animations) {
        if (element.contains(event.target)) animation.cancel();
      }
    };
    const cancel = () => animations.forEach((animation) => animation.cancel());
    const onPreferenceChange = () => { if (reducedMotion.matches) cancel(); };
    node.addEventListener("focusin", revealFocused);
    reducedMotion.addEventListener("change", onPreferenceChange);
    return () => {
      node.removeEventListener("focusin", revealFocused);
      reducedMotion.removeEventListener("change", onPreferenceChange);
      cancel();
    };
  }, [view, onMount, direction]);

  return region;
}
