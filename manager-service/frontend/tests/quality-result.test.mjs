import test from "node:test";
import assert from "node:assert/strict";
import { currentQuality, qualitySummary } from "../src/quality-result.ts";

test("quality evidence is scoped to the exact uploaded content", () => {
  const result = { source_hash: "old", status: "analyzed" };
  assert.equal(currentQuality({ hash: "new" }, result), undefined);
  assert.equal(currentQuality({ hash: "old" }, result), result);
  assert.equal(currentQuality({ hash: "old" }), undefined);
});

test("incomplete and failed analyses are never described as passed", () => {
  for (const [status, text] of [
    ["error", "检测失败"],
    ["unsupported", "暂不支持"],
    ["inconclusive", "无法判断"],
  ]) {
    assert.equal(qualitySummary({ status }), text);
  }
  assert.equal(
    qualitySummary({
      status: "analyzed",
      upscaled: true,
      bit_depth_method: "grid",
    }),
    "疑似扩位",
  );
  assert.equal(
    qualitySummary({ status: "analyzed", possible_upsampling: true }),
    "疑似升频",
  );
  assert.equal(qualitySummary({ status: "analyzed" }), "未发现异常");
});
