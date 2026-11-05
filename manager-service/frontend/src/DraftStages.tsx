import { useEffect, useState } from "react";
import { Chip, Tooltip } from "@mui/material";
import { alpha } from "@mui/material/styles";
import { useAppActions, useAppState } from "./api";
import { draftTitle, type Draft } from "./types";
import { draftStage, type DraftStageKind } from "./draft-stage";
import { DraftStageIcon } from "./DraftStageIcon";

function DraftStageBadge({
  batch,
  kind,
  disabled,
}: {
  batch: Draft;
  kind: DraftStageKind;
  disabled: boolean;
}) {
  const { state } = useAppState();
  const { enqueue } = useAppActions();
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  useEffect(
    () => setSubmitError(""),
    [
      batch.read_state,
      batch.read_error,
      batch.quality_state,
      batch.quality_error,
    ],
  );
  const stage = draftStage(batch, kind, state?.jobs || []);
  const error = submitError || stage.error;
  const phase = submitting || error ? "queued" : stage.phase;
  const prefix =
    phase === "running" ? "正在" : phase === "done" ? "完成" : "等待";
  const label = `${prefix}${stage.shortOperation}`;
  const canClick =
    !disabled &&
    !submitting &&
    !stage.busy &&
    batch.files.length > 0 &&
    (kind === "preview" || batch.previewed);
  async function run() {
    if (!canClick) return;
    setSubmitError("");
    setSubmitting(true);
    try {
      await enqueue(`drafts/${batch.id}/${kind}`);
    } catch (error) {
      setSubmitError(
        error instanceof Error ? error.message : "请求失败，请点击标签重试",
      );
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Tooltip
      title={
        error ||
        (canClick ? (kind === "preview" ? "重读覆盖" : "重新检测") : label)
      }
    >
      <span>
        <Chip
          component="button"
          type="button"
          className="draft-stage-chip"
          data-stage={kind}
          data-phase={phase}
          data-error={!!error}
          size="small"
          label={label}
          aria-label={`${prefix}${stage.operation}，${draftTitle(batch)}`}
          aria-busy={phase === "running"}
          disabled={!canClick}
          onClick={(event) => {
            event.stopPropagation();
            void run();
          }}
          icon={<DraftStageIcon state={error ? "error" : phase} kind={kind} />}
          sx={{
            height: 24,
            borderRadius: 1,
            color: error ? "warning.light" : "primary.light",
            bgcolor: (theme) =>
              alpha(
                error ? theme.palette.warning.main : theme.palette.primary.main,
                0.1,
              ),
            "& .MuiChip-icon": { color: "inherit" },
            "&.Mui-disabled": { opacity: 1 },
            "& .MuiChip-label": { px: 0.75 },
            "&:not(.Mui-disabled):hover": {
              bgcolor: "var(--page-accent-selected)",
            },
          }}
        />
      </span>
    </Tooltip>
  );
}

export function DraftStages({
  batch,
  disabled = false,
}: {
  batch: Draft;
  disabled?: boolean;
}) {
  return (
    <>
      <DraftStageBadge batch={batch} kind="preview" disabled={disabled} />
      <DraftStageBadge batch={batch} kind="quality" disabled={disabled} />
    </>
  );
}
