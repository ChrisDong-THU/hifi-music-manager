import type { QualityResult, Track } from "./types";

export function currentQuality(track: Track, result?: QualityResult) {
  return result?.source_hash === track.hash ? result : undefined;
}

export function qualitySummary(result: QualityResult) {
  if (result.status === "error") return "检测失败";
  if (result.status === "unsupported") return "暂不支持";
  if (result.upscaled && result.possible_upsampling)
    return "位深不足 · 疑似升频";
  if (result.upscaled)
    return result.bit_depth_method === "grid" ? "疑似扩位" : "位深不足";
  if (result.possible_upsampling) return "疑似升频";
  if (result.status === "inconclusive") return "无法判断";
  return "未发现异常";
}
