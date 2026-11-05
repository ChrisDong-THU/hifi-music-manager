import { Box, Card, Tooltip, Typography } from "@mui/material";
import { CircleAlert, History, LoaderCircle } from "lucide-react";
import { useAppState } from "./api";
import { Empty, OperationIcon } from "./components";
import { SectionHeader } from "./upstream/primitives";
import { ScrollableCardContent } from "./ScrollableCardContent";
import type { Operation } from "./types";

const dayFormat = new Intl.DateTimeFormat("zh-CN", {
  month: "2-digit",
  day: "2-digit",
});
const timeFormat = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

function HistoryRow({ operation: op }: { operation: Operation }) {
  const date = new Date(op.created);
  const failed = op.state === "failed";
  const working = op.state === "working";
  const fullLabel = `${op.label}${op.affected ? ` · ${op.affected} 项` : ""}`;
  const statusHint = failed
    ? `${fullLabel}\n失败：${op.error || "操作未完成，请重试"}`
    : working
      ? `${fullLabel}\n正在进行`
      : "";
  return (
    <Tooltip
      title={statusHint}
      describeChild
      placement="top-start"
      enterDelay={300}
      slotProps={{
        tooltip: {
          sx: {
            maxWidth: 420,
            whiteSpace: "pre-line",
            overflowWrap: "anywhere",
          },
        },
      }}
    >
      <Box
        component="li"
        tabIndex={failed ? 0 : undefined}
        aria-label={
          failed
            ? `${op.label}，失败`
            : working
              ? `${op.label}，进行中`
              : undefined
        }
        sx={{
          display: "grid",
          gridTemplateColumns: "18px minmax(0, 1fr) auto",
          alignItems: "center",
          columnGap: 1.5,
          height: 38,
          boxSizing: "border-box",
          px: 1,
          py: 0.75,
          borderBottom: "1px solid rgba(255, 255, 255, 0.055)",
          "&:last-child": { borderBottom: 0 },
          "&:focus-visible": {
            outline: "1px solid",
            outlineColor: "error.light",
            outlineOffset: -1,
            borderRadius: 0.5,
          },
          "&:hover": { bgcolor: "rgba(255, 255, 255, 0.025)" },
        }}
      >
        <Box
          aria-hidden
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            width: 18,
            height: 18,
            color: failed
              ? "error.light"
              : working
                ? "primary.main"
                : "primary.light",
            opacity: failed || working ? 0.9 : 0.65,
            animation: working
              ? "history-status-spin 1.4s linear infinite"
              : "none",
            "@keyframes history-status-spin": {
              to: { transform: "rotate(360deg)" },
            },
            "@media (prefers-reduced-motion: reduce)": { animation: "none" },
          }}
        >
          {failed ? (
            <CircleAlert size={16} />
          ) : working ? (
            <LoaderCircle size={16} />
          ) : (
            <OperationIcon kind={op.kind} />
          )}
        </Box>
        <Box
          sx={{ display: "flex", alignItems: "baseline", gap: 1, minWidth: 0 }}
        >
          <Typography
            variant="body2"
            noWrap
            title={failed || working ? undefined : fullLabel}
            sx={{
              fontSize: 13,
              lineHeight: 1.5,
              color: failed ? "error.light" : "text.primary",
            }}
          >
            {op.label}
          </Typography>
          {op.skipped ? (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ flexShrink: 0, fontSize: 11 }}
            >
              跳过 {op.skipped}
            </Typography>
          ) : null}
        </Box>
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            gap: 1.5,
            whiteSpace: "nowrap",
          }}
        >
          <Typography
            component="time"
            dateTime={op.created}
            title={
              failed || working
                ? undefined
                : date.toLocaleString("zh-CN", { hour12: false })
            }
            color="text.secondary"
            sx={{
              display: "flex",
              gap: 0.75,
              fontSize: 11,
              fontVariantNumeric: "tabular-nums",
              opacity: 0.8,
            }}
          >
            <Box
              component="span"
              sx={{ display: { mobile: "none", tablet: "inline" } }}
            >
              {dayFormat.format(date)}
            </Box>
            <span>{timeFormat.format(date)}</span>
          </Typography>
        </Box>
      </Box>
    </Tooltip>
  );
}

export function HistoryCard() {
  const { state } = useAppState();
  if (!state) return null;
  return (
    <Card
      className="home-history-card scroll-panel"
      component="section"
      aria-label="操作记录"
      sx={{ p: { mobile: 1, tablet: 2 } }}
    >
      <Box className="panel-header">
        <SectionHeader icon={<History size={24} />} title="操作记录" />
      </Box>
      <ScrollableCardContent className="home-history-list" label="操作记录列表">
        {state.operations.length ? (
          <Box component="ul" sx={{ m: 0, p: 0, listStyle: "none" }}>
            {state.operations.map((op) => (
              <HistoryRow key={op.id} operation={op} />
            ))}
          </Box>
        ) : (
          <Empty kind="history">暂无记录</Empty>
        )}
      </ScrollableCardContent>
    </Card>
  );
}
