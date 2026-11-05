import { CircularProgress } from "@mui/material";
import { CircleAlert, ScanSearch, Tags } from "lucide-react";
import { GrowingRipple } from "./upstream/GrowingRipple";
import type { DraftStageKind } from "./draft-stage";
import { AnimatedStrokeIcon } from "./AnimatedStrokeIcon";

export function DraftStageIcon({
  state,
  kind,
  className,
}: {
  state: "queued" | "running" | "done" | "error";
  kind: DraftStageKind;
  className?: string;
}) {
  const Icon =
    state === "error" ? CircleAlert : kind === "preview" ? Tags : ScanSearch;
  return (
    <AnimatedStrokeIcon iconKey={`${kind}:${state}`} className={className}>
      {state === "queued" ? (
        <GrowingRipple size={16} />
      ) : state === "running" ? (
        <CircularProgress size={16} color="inherit" />
      ) : (
        <Icon size={16} />
      )}
    </AnimatedStrokeIcon>
  );
}
