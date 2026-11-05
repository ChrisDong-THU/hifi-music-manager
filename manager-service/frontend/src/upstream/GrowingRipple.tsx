// Adapted from beets-flask components/common/loading.tsx (MIT, 00b246d).
import { Box } from "@mui/material";

export function GrowingRipple({
  size,
  color,
  className,
}: {
  size?: string | number;
  color?: string;
  className?: string;
}) {
  return (
    <Box
      className={className}
      aria-hidden="true"
      sx={{
        color: color || "inherit",
        display: "inline-block",
        position: "relative",
        width: size,
        height: size,
        div: {
          position: "absolute",
          border: "2px solid currentColor",
          borderRadius: "50%",
          animation: "ripple 3s cubic-bezier(0, 0.2, 0.8, 1) infinite",
        },
        "div:nth-of-type(2)": {
          animationDelay: "-1s",
        },
        "@keyframes ripple": {
          "0%, 50%": {
            top: "50%",
            left: "50%",
            width: 0,
            height: 0,
            opacity: 0,
          },
          "90%": {
            top: 0,
            left: 0,
            width: "100%",
            height: "100%",
            opacity: 0.7,
          },
          "100%": {
            top: 0,
            left: 0,
            width: "100%",
            height: "100%",
            opacity: 0,
          },
        },
        "@media (prefers-reduced-motion: reduce)": {
          div: {
            animation: "none",
            top: "25%",
            left: "25%",
            width: "50%",
            height: "50%",
            opacity: 0.7,
          },
          "div:nth-of-type(2)": { display: "none" },
        },
      }}
    >
      <div />
      <div />
    </Box>
  );
}
