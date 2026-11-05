import { CardContent } from "@mui/material";
import type { ReactNode } from "react";

/** Shared spacing, keyboard focus and scrollbar behavior for bounded lists. */
export function ScrollableCardContent({
  label,
  id,
  hidden,
  className = "",
  children,
}: {
  label: string;
  id?: string;
  hidden?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <CardContent
      id={id}
      hidden={hidden}
      className={`panel-scroll ${className}`}
      role="region"
      aria-label={label}
      tabIndex={0}
      sx={{ px: { mobile: 0.5, tablet: 1 }, pt: 1.5, mt: 1, pb: "0!important" }}
    >
      {children}
    </CardContent>
  );
}
