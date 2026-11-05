import { useState } from "react";
import { Box, ButtonBase, Typography } from "@mui/material";
import { Modal } from "./components";
import {
  audioQuality,
  filename,
  type Track,
  type QualityResult,
} from "./types";
import { currentQuality, qualitySummary } from "./quality-result";

export function AudioQuality({
  track,
  result: stored,
  running = false,
}: {
  track: Track;
  result?: QualityResult;
  running?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const result = currentQuality(track, stored);
  const label = audioQuality(track);
  if (!result)
    return (
      <Box
        component="span"
        className="file-row-quality"
        aria-label="音源检测状态"
      >
        {running ? "检测中" : "未检测"}
      </Box>
    );
  const summary = qualitySummary(result);
  const warning =
    result.upscaled || result.possible_upsampling || result.status === "error";
  const rows = [
    [
      "文件标称",
      result.sample_rate && result.declared_bits
        ? `${result.sample_rate / 1000} kHz / ${result.declared_bits} bit`
        : label,
    ],
    [
      "有效位深",
      result.effective_bits
        ? `${result.bit_depth_method === "grid" ? "约 " : ""}${result.effective_bits} bit${result.bit_depth_method === "grid" ? "（量化网格估计）" : "（全曲整数样本）"}`
        : "无法判断",
    ],
    [
      "样本占用位数",
      result.stored_bits ? `${result.stored_bits} bit` : "无法判断",
    ],
    [
      "频谱截止",
      result.cutoff_hz
        ? `约 ${(result.cutoff_hz / 1000).toFixed(1)} kHz`
        : "无法判断",
    ],
    [
      "升频检查",
      (result.sample_rate || 0) <= 48000
        ? "此检查仅适用于高于 48 kHz 的音频"
        : result.spectral_windows < 12
          ? "有效分析片段不足"
          : result.possible_upsampling
            ? "存在疑似升频特征"
            : "未发现疑似升频特征",
    ],
  ];
  return (
    <>
      <ButtonBase
        className="file-row-quality quality-result-button"
        data-warning={warning}
        aria-label={`音源检测：${summary}，${track.tags.title || track.path}`}
        title={`${summary}，点击查看详情`}
        onClick={(event) => {
          event.stopPropagation();
          setOpen(true);
        }}
      >
        {summary}
      </ButtonBase>
      {open ? (
        <Modal
          title={
            <Box
              component="span"
              title={filename(track.path)}
              sx={{
                display: "block",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {filename(track.path)}
            </Box>
          }
          open
          onClose={() => setOpen(false)}
        >
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <Typography color={warning ? "warning.light" : "text.primary"}>
              {result.message}
            </Typography>
            {result.status !== "unsupported" && result.status !== "error" ? (
              <Box
                component="dl"
                sx={{
                  m: 0,
                  display: "grid",
                  gridTemplateColumns: "auto minmax(0, 1fr)",
                  columnGap: 3,
                  rowGap: 1.5,
                }}
              >
                {rows.map(([name, value]) => (
                  <Box key={name} sx={{ display: "contents" }}>
                    <Typography
                      component="dt"
                      variant="body2"
                      color="text.secondary"
                    >
                      {name}
                    </Typography>
                    <Typography component="dd" variant="body2" sx={{ m: 0 }}>
                      {value}
                    </Typography>
                  </Box>
                ))}
              </Box>
            ) : null}
          </Box>
        </Modal>
      ) : null}
    </>
  );
}
