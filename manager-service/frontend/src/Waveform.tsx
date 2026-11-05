// Beets-flask's waveform.tsx visual structure, adapted to browser-side decoding.
import { useEffect, useId, useState } from "react";
import { Box } from "@mui/material";

const bins = 1024;
const maxBytes = 128 * 1024 * 1024;
const waveforms = new Map<string, string | null>();
let decoding: Promise<unknown> = Promise.resolve();

// Keep one decode in flight and cache only the small waveform, never audio data.
function loadWaveform(url: string, key: string, signal: AbortSignal) {
  const task = decoding.then(async () => {
    signal.throwIfAborted();
    if (waveforms.has(key)) return waveforms.get(key) ?? null;
    const response = await fetch(url, { signal });
    if (
      !response.ok ||
      Number(response.headers.get("Content-Length")) > maxBytes
    ) {
      await response.body?.cancel();
      return null;
    }
    const reader = response.body?.getReader();
    if (!reader) return null;
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    let size = 0;
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        size += value.byteLength;
        if (size > maxBytes) {
          await reader.cancel();
          return null;
        }
        chunks.push(value);
      }
    } finally {
      reader.releaseLock();
    }
    signal.throwIfAborted();
    const encoded = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      encoded.set(chunk, offset);
      offset += chunk.length;
    }
    chunks.length = 0;
    // A low sample rate is sufficient for an envelope and bounds decoded memory.
    const context = new OfflineAudioContext(1, 1, 8000);
    const audio = await context.decodeAudioData(encoded.buffer);
    signal.throwIfAborted();
    if (audio.duration > 1800) return null;
    const peaks = new Float32Array(bins);
    for (let channel = 0; channel < audio.numberOfChannels; channel++) {
      const samples = audio.getChannelData(channel);
      for (let bin = 0; bin < bins; bin++) {
        const start = Math.floor((bin * samples.length) / bins);
        const end = Math.floor(((bin + 1) * samples.length) / bins);
        let peak = peaks[bin];
        for (let i = start; i < end; i++)
          peak = Math.max(peak, Math.abs(samples[i]));
        peaks[bin] = peak;
      }
    }
    const scale = 23 / (Math.max(...peaks) || 1);
    let path = "M0 24";
    for (let i = 0; i < bins; i++)
      path += `L${i} ${(24 - peaks[i] * scale).toFixed(2)}`;
    for (let i = bins - 1; i >= 0; i--)
      path += `L${i} ${(24 + peaks[i] * scale).toFixed(2)}`;
    path += "Z";
    if (waveforms.size >= 12) waveforms.delete(waveforms.keys().next().value!);
    waveforms.set(key, path);
    return path;
  });
  decoding = task.catch(() => {});
  return task;
}

export function useWaveform(
  url: string,
  key: string,
  duration: number,
  enabled: boolean,
) {
  const [result, setResult] = useState<{ key: string; path: string | null }>();
  useEffect(() => {
    if (!enabled || duration > 1800) return;
    const controller = new AbortController();
    void loadWaveform(url, key, controller.signal)
      .then((path) => {
        if (!controller.signal.aborted) setResult({ key, path });
      })
      .catch(() => {
        if (controller.signal.aborted) return;
        if (waveforms.size >= 12)
          waveforms.delete(waveforms.keys().next().value!);
        waveforms.set(key, null);
        setResult({ key, path: null });
      });
    return () => controller.abort();
  }, [url, key, duration, enabled]);
  return result?.key === key ? result.path : null;
}

export function playerTime(seconds: number) {
  const value = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0;
  return `${Math.floor(value / 60)}:${String(value % 60).padStart(2, "0")}`;
}

export function WaveformSeek({
  path,
  time,
  duration,
  buffered,
  ready,
  onSeek,
  height = 48,
}: {
  path: string | null;
  time: number;
  duration: number;
  buffered: number;
  ready: boolean;
  onSeek: (seconds: number) => void;
  height?: number;
}) {
  const clip = useId().replaceAll(":", "");
  const progress = duration > 0 ? Math.min(1, Math.max(0, time / duration)) : 0;
  const loaded =
    duration > 0 ? Math.min(1, Math.max(progress, buffered / duration)) : 0;
  return (
    <Box
      className="player-waveform"
      data-waveform={path ? "ready" : "progress"}
      sx={{
        height,
        color: "primary.main",
        "& .wave-unplayed": { fill: (theme) => theme.palette.primary.muted },
      }}
    >
      {path ? (
        <svg
          viewBox={`0 0 ${bins} 48`}
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          <defs>
            <clipPath id={clip}>
              <rect width={progress * bins} height="48" />
            </clipPath>
          </defs>
          <path className="wave-unplayed" d={path} />
          <path
            className="wave-played"
            fill="currentColor"
            d={path}
            clipPath={`url(#${clip})`}
          />
        </svg>
      ) : (
        <Box
          className="player-waveform-fallback"
          sx={{ bgcolor: "primary.muted" }}
        >
          <Box
            className="wave-played"
            sx={{
              width: `${progress * 100}%`,
              height: "100%",
              bgcolor: "currentColor",
            }}
          />
        </Box>
      )}
      {path ? (
        <Box
          className="player-waveform-unbuffered"
          sx={{ left: `${loaded * 100}%` }}
        />
      ) : null}
      <Box
        className="player-waveform-cursor"
        sx={{ left: `${progress * 100}%` }}
      />
      <input
        type="range"
        aria-label="播放进度"
        aria-valuetext={`${playerTime(time)} / ${playerTime(duration)}`}
        min={0}
        max={duration || 1}
        step={0.1}
        value={Math.min(time, duration || 1)}
        disabled={!ready || duration <= 0}
        onChange={(event) => onSeek(Number(event.target.value))}
      />
    </Box>
  );
}
