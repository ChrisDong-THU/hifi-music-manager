import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { Box, useMediaQuery } from "@mui/material";
import {
  createStrokeMotion,
  iconExitRetention,
  iconMotion,
} from "./stroke-motion";
import "./animated-stroke-icon.css";

type IconFrame = {
  id: number;
  key: string;
  content: ReactNode;
  color: string;
  entering: boolean;
};

function StrokeLayer({
  frame,
  leaving,
  reducedMotion,
  children,
}: {
  frame: IconFrame;
  leaving: boolean;
  reducedMotion: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const controller = useRef<ReturnType<typeof createStrokeMotion> | null>(null);
  const [entered, setEntered] = useState(!frame.entering || reducedMotion);
  useLayoutEffect(() => {
    const motion = createStrokeMotion(ref.current!);
    controller.current = motion;
    return () => {
      motion.dispose();
      controller.current = null;
    };
  }, []);
  useLayoutEffect(() => {
    const motion = controller.current!;
    motion.update(
      leaving ? "leave" : frame.entering ? "enter" : null,
      reducedMotion,
    );
    if (reducedMotion) setEntered(true);
    return () => motion.interrupt();
  }, [frame.entering, leaving, reducedMotion]);
  return (
    <Box
      component="span"
      ref={ref}
      className="stroke-icon-layer"
      data-leaving={leaving}
      sx={{ color: frame.color }}
    >
      <span
        className="stroke-icon-content"
        data-entering={frame.entering && !entered}
        onAnimationEnd={(event) => {
          if (event.target === event.currentTarget) setEntered(true);
        }}
      >
        {children}
      </span>
    </Box>
  );
}

/** iconKey must change whenever the icon geometry or loading mode changes. */
export function AnimatedStrokeIcon({
  iconKey,
  children,
  color = "inherit",
  size = 16,
  motion = "standard",
  className = "",
}: {
  iconKey: string;
  children: ReactNode;
  color?: string;
  size?: number;
  motion?: "standard" | "subtle";
  className?: string;
}) {
  const reducedMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const [frames, setFrames] = useState<{
    current: IconFrame;
    previous: IconFrame | null;
  }>({
    current: { id: 0, key: iconKey, content: children, color, entering: false },
    previous: null,
  });
  // Two keyed layers bound both DOM size and interrupted transitions. Updating
  // derived state during render avoids painting the new geometry in the old slot.
  if (frames.current.key !== iconKey) {
    setFrames({
      current: {
        id: frames.current.id + 1,
        key: iconKey,
        content: children,
        color,
        entering: true,
      },
      previous: frames.current,
    });
  } else if (frames.current.color !== color) {
    setFrames({
      ...frames,
      current: { ...frames.current, color, content: children },
    });
  }
  const previousID = frames.previous?.id;
  useEffect(() => {
    if (previousID === undefined) return;
    const timer = window.setTimeout(
      () =>
        setFrames((value) =>
          value.previous?.id === previousID
            ? { ...value, previous: null }
            : value,
        ),
      reducedMotion ? 0 : iconExitRetention,
    );
    return () => window.clearTimeout(timer);
  }, [previousID, reducedMotion]);
  const style = {
    width: size,
    height: size,
    "--stroke-icon-enter-duration": `${iconMotion.enter.layer}ms`,
    "--stroke-icon-leave-duration": `${iconMotion.leave.layer}ms`,
    "--stroke-icon-enter-easing": iconMotion.enter.easing,
    "--stroke-icon-leave-easing": iconMotion.leave.easing,
  } as CSSProperties;
  return (
    <span
      className={`stroke-icon-slot ${className}`}
      style={style}
      aria-hidden="true"
      data-icon-key={iconKey}
      data-motion={motion}
      data-reduced-motion={reducedMotion}
    >
      {frames.previous && !reducedMotion ? (
        <StrokeLayer
          key={frames.previous.id}
          frame={frames.previous}
          leaving
          reducedMotion={false}
        >
          {frames.previous.content}
        </StrokeLayer>
      ) : null}
      <StrokeLayer
        key={frames.current.id}
        frame={frames.current}
        leaving={false}
        reducedMotion={reducedMotion}
      >
        {children}
      </StrokeLayer>
    </span>
  );
}
