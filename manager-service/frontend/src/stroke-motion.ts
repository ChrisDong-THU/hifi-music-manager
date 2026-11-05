export const iconMotion = {
  enter: {
    layer: 320,
    stroke: 420,
    delay: 50,
    stagger: 28,
    staggerLimit: 5,
    easing: "cubic-bezier(0.22, 0.61, 0.36, 1)",
  },
  leave: {
    layer: 220,
    stroke: 180,
    delay: 0,
    stagger: 12,
    staggerLimit: 4,
    easing: "cubic-bezier(0.4, 0, 1, 1)",
  },
} as const;

// Retain the outgoing layer until both its wrapper and longest stroke finish.
export const iconExitRetention =
  Math.max(
    iconMotion.leave.layer,
    iconMotion.leave.stroke +
      iconMotion.leave.delay +
      iconMotion.leave.stagger * iconMotion.leave.staggerLimit,
  ) + 16;

type Phase = "enter" | "leave" | null;
const properties = [
  "stroke-dasharray",
  "stroke-dashoffset",
  "opacity",
] as const;
const geometrySelector =
  ".lucide > path, .lucide > circle, .lucide > line, .lucide > polyline, .lucide > polygon, .lucide > rect, .lucide > ellipse";
type Stroke = {
  element: SVGGeometryElement;
  length: number;
  original: { property: string; value: string; priority: string }[];
};

// One controller per keyed layer. Loading rings and pending ripples keep their
// own loops; only Lucide geometry participates in the stroke transition.
export function createStrokeMotion(root: HTMLElement) {
  let phase: Phase | undefined;
  let disposed = false;
  let strokes: Stroke[] | undefined;
  const active = new Map<SVGGeometryElement, Animation>();

  function restore(stroke: Stroke) {
    for (const { property, value, priority } of stroke.original) {
      if (value) stroke.element.style.setProperty(property, value, priority);
      else stroke.element.style.removeProperty(property);
    }
  }

  function stop(preserve: boolean) {
    // Read all interrupted positions before writing any styles.
    const snapshots = Array.from(active, ([element, animation]) => {
      const style = preserve ? getComputedStyle(element) : null;
      return {
        element,
        animation,
        values: style
          ? properties.map((property) => style.getPropertyValue(property))
          : [],
      };
    });
    active.clear();
    for (const { element, animation, values } of snapshots) {
      animation.onfinish = null;
      animation.cancel();
      if (preserve)
        properties.forEach((property, index) =>
          element.style.setProperty(property, values[index]),
        );
    }
    if (!preserve) strokes?.forEach(restore);
  }

  return {
    update(nextPhase: Phase, reducedMotion: boolean) {
      if (disposed) return;
      const changed = phase !== nextPhase;
      phase = nextPhase;
      if (reducedMotion || !nextPhase) {
        stop(false);
        return;
      }
      // Polling and re-enabling motion cannot replay an already settled phase.
      if (!changed) return;
      stop(true);
      strokes ??= Array.from(
        root.querySelectorAll<SVGGeometryElement>(geometrySelector),
        (element) => ({
          element,
          length: element.getTotalLength(),
          original: properties.map((property) => ({
            property,
            value: element.style.getPropertyValue(property),
            priority: element.style.getPropertyPriority(property),
          })),
        }),
      );
      const leaving = nextPhase === "leave";
      const timing = iconMotion[nextPhase];
      const frames = strokes.map(({ element, length }) => {
        const current = getComputedStyle(element);
        const offset = Math.min(
          length,
          Math.max(0, parseFloat(current.strokeDashoffset) || 0),
        );
        return length < 1
          ? [
              { opacity: leaving ? current.opacity : 0 },
              { opacity: leaving ? 0 : 1 },
            ]
          : [
              {
                strokeDasharray: `${length} ${length}`,
                strokeDashoffset: leaving ? offset : length,
              },
              {
                strokeDasharray: `${length} ${length}`,
                strokeDashoffset: leaving ? length : 0,
              },
            ];
      });
      strokes.forEach((stroke, index) => {
        const animation = stroke.element.animate(frames[index], {
          duration: timing.stroke,
          delay:
            timing.delay +
            Math.min(index, timing.staggerLimit) * timing.stagger,
          easing: timing.easing,
          fill: "both",
        });
        active.set(stroke.element, animation);
        if (!leaving) {
          animation.onfinish = () => {
            if (active.get(stroke.element) !== animation) return;
            active.delete(stroke.element);
            animation.onfinish = null;
            animation.cancel();
            restore(stroke);
          };
        }
      });
    },
    interrupt() {
      if (!disposed) stop(true);
    },
    dispose() {
      if (disposed) return;
      stop(false);
      disposed = true;
    },
  };
}
