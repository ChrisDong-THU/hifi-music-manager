// Layout adapted from beets-flask components/library/audio/player.tsx (MIT).
import {
  forwardRef,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  Box,
  ButtonBase,
  Dialog,
  Divider,
  IconButton,
  Paper,
  Slide,
  Slider,
  Tooltip,
  Typography,
  useMediaQuery,
} from "@mui/material";
import type { TransitionProps } from "@mui/material/transitions";
import {
  ChevronDown,
  Clock3,
  Pause,
  Play,
  SkipBack,
  SkipForward,
  Square,
  Volume1,
  Volume2,
  VolumeX,
  X,
} from "lucide-react";
import { useAppActions, useLibraryState } from "./api";
import { libraryMediaURL } from "./http-client";
import { usePlayback } from "./playback";
import { useCoverSource } from "./Cover";
import type { Playback } from "./types";
import { filename } from "./types";
import { playerTime, useWaveform, WaveformSeek } from "./Waveform";

const PlayerTransition = forwardRef(function PlayerTransition(
  props: TransitionProps & { children: ReactElement },
  ref: React.Ref<unknown>,
) {
  return <Slide direction="up" ref={ref} {...props} />;
});

function PlayerButton({
  label,
  children,
  onClick,
  disabled,
  primary = false,
}: {
  label: string;
  children: ReactNode;
  onClick: () => void;
  disabled?: boolean;
  primary?: boolean;
}) {
  return (
    <Tooltip title={label}>
      <span>
        <IconButton
          aria-label={label}
          onClick={onClick}
          disabled={disabled}
          className={primary ? "player-play-pause" : undefined}
          sx={
            primary
              ? {
                  bgcolor: "primary.muted",
                  "&:hover": { bgcolor: "primary.dark" },
                }
              : undefined
          }
        >
          {children}
        </IconButton>
      </span>
    </Tooltip>
  );
}

function PlayerCover({
  id,
  className,
  draft = false,
}: {
  id: string;
  className?: string;
  draft?: boolean;
}) {
  const [failed, setFailed] = useState("");
  const source = useCoverSource(id);
  return (
    <Box
      component="img"
      className={`player-cover ${className || ""}`}
      alt="专辑封面"
      src={draft || failed === source ? "/default-cover.svg" : source}
      onError={() => setFailed(source)}
    />
  );
}

function VolumeControl({
  volume,
  muted,
  onVolume,
  onMute,
}: {
  volume: number;
  muted: boolean;
  onVolume: (volume: number) => void;
  onMute: () => void;
}) {
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const keyboardFocus = useRef(false);
  const dragging = useRef(false);
  const panelId = useId();
  const silent = muted || volume === 0;
  const level = muted ? 0 : Math.round(volume * 100);
  return (
    <Box
      ref={container}
      className="player-volume-control"
      role="group"
      aria-label="音量控制"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => {
        if (!keyboardFocus.current && !dragging.current) setOpen(false);
      }}
      onPointerDownCapture={() => {
        keyboardFocus.current = false;
      }}
      onFocusCapture={(event) => {
        if (event.target.matches(":focus-visible")) {
          keyboardFocus.current = true;
          setOpen(true);
        }
      }}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          keyboardFocus.current = false;
          if (!dragging.current) setOpen(false);
        }
      }}
      onKeyDownCapture={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          event.preventDefault();
          button.current?.focus();
          keyboardFocus.current = false;
          setOpen(false);
        } else {
          keyboardFocus.current = true;
          setOpen(true);
        }
      }}
    >
      <IconButton
        ref={button}
        aria-label={silent ? "恢复声音" : "静音"}
        aria-pressed={silent}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={onMute}
        sx={{ color: silent ? "primary.main" : "text.primary" }}
      >
        {silent ? (
          <VolumeX size={20} />
        ) : volume < 0.5 ? (
          <Volume1 size={20} />
        ) : (
          <Volume2 size={20} />
        )}
      </IconButton>
      <Box
        id={panelId}
        className="player-volume-panel"
        aria-hidden={!open}
        sx={{
          visibility: open ? "visible" : "hidden",
          opacity: open ? 1 : 0,
          pointerEvents: open ? "auto" : "none",
        }}
      >
        <Paper className="player-volume-surface" elevation={8}>
          <Slider
            orientation="vertical"
            aria-label="音量"
            min={0}
            max={100}
            value={level}
            onChange={(_, value) => onVolume((value as number) / 100)}
            onPointerDown={() => {
              dragging.current = true;
            }}
            onChangeCommitted={() => {
              dragging.current = false;
              if (
                !keyboardFocus.current &&
                !container.current?.matches(":hover")
              )
                setOpen(false);
            }}
            size="small"
            sx={{
              height: 112,
              "& .MuiSlider-thumb": { width: 10, height: 10 },
            }}
          />
          <Typography
            component="output"
            aria-label="当前音量"
            variant="caption"
            className="player-volume-value"
          >
            {level}
          </Typography>
        </Paper>
      </Box>
    </Box>
  );
}

export function Player({
  playback,
  onChange,
  onClose,
}: {
  playback: Playback;
  onChange: (playback: Playback) => void;
  onClose: () => void;
}) {
  const { audio, playing, setPlaying, togglePlayback } = usePlayback();
  const previousPlayback = useRef(playback);
  const titleId = useId();
  const { session } = useLibraryState();
  const { notify } = useAppActions();
  const mobile = useMediaQuery((theme) => theme.breakpoints.down("tablet"));
  const [expanded, setExpanded] = useState(false);
  const [ready, setReady] = useState(false);
  const [time, setTime] = useState(0);
  const [length, setLength] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [volume, setVolume] = useState(1);
  const [muted, setMuted] = useState(false);
  const { track, queue, batchID } = playback;
  const index = queue.findIndex((item) => item.id === track.id);
  const title = track.tags.title || filename(track.path);
  const artist = track.tags.artist || "未知歌手";
  const audioUrl = libraryMediaURL(
    batchID
      ? `/api/drafts/${batchID}/files/${track.id}/audio`
      : `/api/tracks/${track.id}/audio`,
    session,
  );
  const total = length || track.duration || 0;
  const path = useWaveform(
    audioUrl,
    track.audio_hash || track.hash || track.id,
    track.duration,
    !mobile || expanded,
  );
  const step = (delta: number) => {
    const next = queue[index + delta];
    if (next) onChange({ ...playback, track: next });
  };
  const seek = (seconds: number) => {
    const node = audio.current;
    if (!node || !Number.isFinite(node.duration) || node.duration <= 0) return;
    const target = Math.min(node.duration, Math.max(0, seconds));
    node.currentTime = target;
    setTime(target);
  };
  useEffect(() => {
    const node = audio.current;
    if (!node) return;
    let cancelled = false;
    setTime(0);
    setLength(0);
    setBuffered(0);
    setPlaying(false);
    setReady(false);
    void node.play().catch((error: unknown) => {
      if (
        !cancelled &&
        error instanceof DOMException &&
        error.name !== "AbortError" &&
        error.name !== "NotAllowedError"
      ) {
        notify("无法播放此音频，请重试", true);
      }
    });
    return () => {
      cancelled = true;
      node.pause();
      node.removeAttribute("src");
      node.load();
    };
  }, [audioUrl, notify]);
  useEffect(() => {
    const previous = previousPlayback.current;
    previousPlayback.current = playback;
    const node = audio.current;
    // Selecting the paused current track resumes it without resetting its source.
    if (
      previous === playback ||
      previous.track.id !== playback.track.id ||
      previous.batchID !== playback.batchID ||
      !node?.paused
    )
      return;
    let cancelled = false;
    void node.play().catch(() => {
      if (!cancelled) notify("无法播放此音频，请重试", true);
    });
    return () => {
      cancelled = true;
    };
  }, [playback, notify]);
  useEffect(() => {
    if (audio.current) {
      audio.current.volume = volume;
      audio.current.muted = muted;
    }
  }, [volume, muted, audioUrl]);
  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    navigator.mediaSession.metadata = new MediaMetadata({
      title,
      artist,
      album: track.tags.album,
    });
    return () => {
      navigator.mediaSession.metadata = null;
      navigator.mediaSession.playbackState = "none";
    };
  }, [title, artist, track.tags.album]);
  useEffect(() => {
    if ("mediaSession" in navigator)
      navigator.mediaSession.playbackState = playing ? "playing" : "paused";
  }, [playing]);

  const playButton = (
    <PlayerButton
      label={playing ? "暂停" : "播放"}
      primary
      onClick={togglePlayback}
    >
      {playing ? <Pause size={24} /> : <Play size={24} />}
    </PlayerButton>
  );
  const previous = (
    <PlayerButton label="上一首" disabled={index <= 0} onClick={() => step(-1)}>
      <SkipBack size={18} />
    </PlayerButton>
  );
  const next = (
    <PlayerButton
      label="下一首"
      disabled={index < 0 || index >= queue.length - 1}
      onClick={() => step(1)}
    >
      <SkipForward size={18} />
    </PlayerButton>
  );
  const volumeControl = (
    <VolumeControl
      volume={volume}
      muted={muted}
      onMute={() => {
        if (volume === 0) {
          setVolume(1);
          setMuted(false);
        } else setMuted((value) => !value);
      }}
      onVolume={(value) => {
        setVolume(value);
        setMuted(false);
      }}
    />
  );
  const waveform = (height = 48) => (
    <WaveformSeek
      path={path}
      time={time}
      duration={total}
      buffered={buffered}
      ready={ready}
      onSeek={seek}
      height={height}
    />
  );
  const trackNumber = `第 ${Math.max(1, index + 1)} / ${queue.length} 首`;
  const timing = (
    <Box className="player-timing">
      <Clock3 size={16} />
      <span>
        {playerTime(time)} / {playerTime(total)}
      </span>
    </Box>
  );

  return (
    <>
      <audio
        key={audioUrl}
        ref={audio}
        src={audioUrl}
        preload="metadata"
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onCanPlay={() => setReady(true)}
        onTimeUpdate={(event) => setTime(event.currentTarget.currentTime)}
        onLoadedMetadata={(event) =>
          setLength(
            Number.isFinite(event.currentTarget.duration)
              ? event.currentTarget.duration
              : 0,
          )
        }
        onProgress={(event) => {
          const ranges = event.currentTarget.buffered;
          setBuffered(ranges.length ? ranges.end(ranges.length - 1) : 0);
        }}
        onEnded={() => {
          setPlaying(false);
          step(1);
        }}
        onError={() => {
          setPlaying(false);
          setReady(false);
          notify("浏览器无法播放此音频格式", true);
        }}
      />
      {mobile ? (
        <Box
          component="section"
          aria-label="音乐播放器"
          className="global-player player-mini"
        >
          <ButtonBase
            className="player-mini-summary"
            aria-label={`展开播放器：${title}`}
            onClick={() => setExpanded(true)}
          >
            <PlayerCover id={track.id} draft={!!batchID} />
            <Box className="player-mini-text">
              <Typography noWrap>{title}</Typography>
              <Typography variant="caption" color="text.secondary" noWrap>
                {artist}
              </Typography>
            </Box>
          </ButtonBase>
          <PlayerButton label="关闭播放器" onClick={onClose}>
            <Square size={18} fill="currentColor" />
          </PlayerButton>
          {playButton}
          <Box
            className="player-mini-progress"
            sx={{ bgcolor: "primary.muted" }}
          >
            <Box
              sx={{
                bgcolor: "primary.main",
                width: `${total ? Math.min(100, (time / total) * 100) : 0}%`,
              }}
            />
          </Box>
        </Box>
      ) : (
        <Box
          component="section"
          aria-label="音乐播放器"
          className="global-player player-desktop"
        >
          <PlayerCover id={track.id} draft={!!batchID} />
          <Box className="player-desktop-body">
            <Box className="player-track-info">
              {timing}
              <Box className="player-title-line" title={`${title} · ${artist}`}>
                <Typography noWrap>{title}</Typography>
                <Typography variant="body2" color="text.secondary" noWrap>
                  {artist}
                </Typography>
              </Box>
              <Typography
                className="player-track-number"
                variant="body2"
                color="text.secondary"
                noWrap
              >
                {trackNumber}
              </Typography>
            </Box>
            <Box className="player-desktop-wave">{waveform()}</Box>
            <Box className="player-desktop-controls">
              {volumeControl}
              {previous}
              {next}
              {playButton}
            </Box>
            <Box className="player-desktop-close">
              <PlayerButton label="关闭播放器" onClick={onClose}>
                <X size={16} />
              </PlayerButton>
            </Box>
          </Box>
        </Box>
      )}
      <Dialog
        fullScreen
        open={mobile && expanded}
        onClose={() => setExpanded(false)}
        slots={{ transition: PlayerTransition }}
        aria-labelledby={titleId}
        slotProps={{ paper: { className: "player-fullscreen" } }}
      >
        <Box className="player-fullscreen-header">
          <PlayerButton label="收起播放器" onClick={() => setExpanded(false)}>
            <ChevronDown size={24} />
          </PlayerButton>
          <Typography variant="body2" color="text.secondary">
            {trackNumber}
          </Typography>
          <PlayerButton label="关闭播放器" onClick={onClose}>
            <X size={20} />
          </PlayerButton>
        </Box>
        <Divider />
        <Box className="player-fullscreen-art">
          <PlayerCover id={track.id} draft={!!batchID} />
          <Typography id={titleId} component="h2" variant="h5" fontWeight={600}>
            {title}
          </Typography>
          <Typography color="text.secondary">{artist}</Typography>
        </Box>
        <Box className="player-fullscreen-bottom">
          {waveform(80)}
          {timing}
          <Box className="player-fullscreen-controls">
            <Box>
              {previous}
              {playButton}
              {next}
            </Box>
            <Box className="player-fullscreen-volume">{volumeControl}</Box>
          </Box>
        </Box>
      </Dialog>
    </>
  );
}
