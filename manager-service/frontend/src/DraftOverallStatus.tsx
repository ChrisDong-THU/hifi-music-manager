import { Box, Tooltip } from "@mui/material";
import { CircleHelp, TriangleAlert, X } from "lucide-react";
import { useAppState } from "./api";
import type { Draft } from "./types";
import { draftOverall } from "./draft-overall";
import { AnimatedStrokeIcon } from "./AnimatedStrokeIcon";

const icons = {
  failed: { Icon: X, size: 18, stroke: 1.8 },
  attention: { Icon: TriangleAlert, size: 19, stroke: 1.65 },
  unverified: { Icon: CircleHelp, size: 19, stroke: 1.6 },
};
type VisibleState = keyof typeof icons;
const colors: Record<VisibleState, string> = {
  failed: "error.light",
  attention: "warning.light",
  unverified: "warning.light",
};

export function DraftOverallStatus({ batch }: { batch: Draft }) {
  const { state } = useAppState();
  const overall = draftOverall(batch, state?.jobs || []);
  const status = overall.state;
  if (status !== "failed" && status !== "attention" && status !== "unverified")
    return null;
  return (
    <Tooltip title={overall.description}>
      <Box
        component="span"
        className="draft-overall-status"
        data-state={overall.state}
        role="img"
        aria-label={`目录歌曲总体状态：${overall.description}`}
        tabIndex={0}
        sx={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          flex: "0 0 24px",
          width: 24,
          height: 24,
          color: colors[status],
          "&:focus-visible": {
            outline: "2px solid",
            outlineColor: "primary.main",
            outlineOffset: 2,
            borderRadius: 1,
          },
        }}
      >
        <DraftOverallIcon state={status} />
      </Box>
    </Tooltip>
  );
}

function DraftOverallIcon({ state }: { state: VisibleState }) {
  const icon = icons[state];
  return (
    <AnimatedStrokeIcon
      iconKey={state}
      color={colors[state]}
      size={20}
      motion="subtle"
    >
      <icon.Icon
        size={icon.size}
        strokeWidth={icon.stroke}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </AnimatedStrokeIcon>
  );
}
