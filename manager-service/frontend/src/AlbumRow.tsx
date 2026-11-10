import { useId, useState } from "react";
import {
  Box,
  Button,
  ButtonBase,
  Collapse,
  Typography,
  useMediaQuery,
} from "@mui/material";
import { ChevronDown, Music4, Pause, Play, SquarePen } from "lucide-react";
import { useAppActions } from "./api";
import { useLibraryPage } from "./useLibraryPage";
import { useProgressNotice } from "./ProgressNotice";
import { usePlayback } from "./playback";
import { Empty, IconAction, LoadError } from "./components";
import { Cover } from "./Cover";
import { HiResBadge } from "./HiResBadge";
import { PlaybackWave } from "./PlaybackWave";
import {
  albumLocationLabel,
  audioQuality,
  filename,
  type Album,
  type Track,
  type Playback,
} from "./types";

function PlaybackNotes({ active }: { active: boolean }) {
  return (
    <span className="playback-notes" data-active={active} aria-hidden="true">
      <span className="playback-note-source">
        <Music4 size={16} />
      </span>
      <span className="playback-note-particle">
        <Music4 size={11} />
      </span>
      <span className="playback-note-particle">
        <Music4 size={13} />
      </span>
      <span className="playback-note-particle">
        <Music4 size={10} />
      </span>
    </span>
  );
}

export function AlbumRow({
  album,
  revision,
  onPlay,
  onEdit,
  onAlbumEdit,
  showEditActions = true,
}: {
  album: Album;
  revision: number;
  onPlay: (value: Playback) => void;
  onEdit: (tracks: Track[]) => void;
  onAlbumEdit: (album: Album) => void;
  showEditActions?: boolean;
}) {
  const { notify } = useAppActions();
  const { playback, playing, togglePlayback } = usePlayback();
  const currentAlbum = Boolean(
    playback && !playback.batchID && playback.track.album_key === album.key,
  );
  const albumPlaying = currentAlbum && playing;
  const [expanded, setExpanded] = useState(false);
  const endpoint = `tracks?album=${encodeURIComponent(album.key)}&limit=100&sort=track`;
  const {
    items: tracks,
    total,
    loading,
    error,
    loaded,
    reload,
    loadMore,
    loadAll,
  } = useLibraryPage("tracks", endpoint, revision, expanded);
  const [preparingPlayback, setPreparingPlayback] = useState(false);
  useProgressNotice(expanded && loading, "加载曲目", album.title || "未分类");
  useProgressNotice(preparingPlayback, "准备播放", album.title || "未分类");
  const reduceMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const panelID = useId();
  const location = albumLocationLabel(album.location);
  async function playAlbum() {
    if (currentAlbum) {
      togglePlayback();
      return;
    }
    if (preparingPlayback) return;
    setPreparingPlayback(true);
    try {
      const queue = await loadAll();
      if (queue[0]) onPlay({ track: queue[0], queue });
      else notify("该专辑暂无可播放曲目", true);
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError"))
        notify(e instanceof Error ? e.message : "专辑播放失败", true);
    } finally {
      setPreparingPlayback(false);
    }
  }
  return (
    <Box className="expandable-album" data-edit-actions={showEditActions}>
      <Box className="album-heading-row">
        <Box className="album-result album-summary" data-expanded={expanded}>
          <ButtonBase
            className="album-cover-play"
            aria-label={`${albumPlaying ? "暂停专辑" : currentAlbum ? "继续播放专辑" : "播放专辑"} ${album.title || "未分类"}${location ? " · " + location : ""}`}
            aria-pressed={albumPlaying}
            data-playing={albumPlaying}
            aria-busy={preparingPlayback}
            disabled={preparingPlayback}
            onClick={() => void playAlbum()}
          >
            <Cover albumKey={album.key} size={44} />
            <Box
              component="span"
              className="album-cover-play-overlay"
              aria-hidden="true"
            >
              <Box component="span" className="album-cover-wave">
                <PlaybackWave active={albumPlaying} />
              </Box>
              <Box component="span" className="album-cover-play-icons">
                <Box
                  component="span"
                  className="album-cover-play-glyph album-cover-play-icon"
                >
                  <Play size={24} fill="currentColor" />
                </Box>
                <Box
                  component="span"
                  className="album-cover-play-glyph album-cover-pause-icon"
                >
                  <Pause size={24} fill="currentColor" />
                </Box>
              </Box>
            </Box>
          </ButtonBase>
          <ButtonBase
            className="album-toggle"
            aria-expanded={expanded}
            aria-busy={expanded && loading}
            aria-controls={panelID}
            onClick={() => setExpanded((value) => !value)}
          >
            <Box sx={{ minWidth: 0, flex: 1 }}>
              <Box className="album-title-line">
                <Typography
                  className="library-entry-title"
                  variant="body1"
                  fontWeight="bold"
                  noWrap
                >
                  {album.title || "未分类"}
                </Typography>
                {album.hi_res ? <HiResBadge /> : null}
              </Box>
              <Typography
                className="library-entry-subtitle"
                variant="body2"
                color="text.secondary"
                title={location}
                noWrap
              >
                {album.artist || "未知歌手"}
                {album.year ? " · " + album.year : ""}
                {location ? " · " + location : ""}
              </Typography>
            </Box>
            <Box
              className="album-track-count"
              sx={{ minWidth: 32, display: "flex", justifyContent: "flex-end" }}
            >
              <Typography variant="caption" color="text.secondary" noWrap>
                {album.count} 首
              </Typography>
            </Box>
            <ChevronDown
              size={20}
              style={{ transform: expanded ? "rotate(180deg)" : "none" }}
            />
          </ButtonBase>
        </Box>
        {showEditActions ? (
          <IconAction
            label={`编辑专辑 ${album.title || "未分类"}${location ? " · " + location : ""}`}
            tooltip="编辑专辑"
            onClick={() => onAlbumEdit(album)}
          >
            <SquarePen size={16} />
          </IconAction>
        ) : null}
      </Box>
      <Collapse
        in={expanded && (loaded || !!error)}
        timeout={reduceMotion ? 0 : { enter: 280, exit: 220 }}
        easing={{
          enter: "cubic-bezier(0.22, 1, 0.36, 1)",
          exit: "cubic-bezier(0.4, 0, 0.2, 1)",
        }}
        unmountOnExit
      >
        <Box
          id={panelID}
          role="region"
          aria-label={(album.title || "未分类") + "曲目"}
          aria-busy={loading}
          className="album-track-list"
        >
          <LoadError error={error} retry={() => void reload()} />
          {!loading && !error && !tracks.length ? (
            <Empty kind="tracks">暂无曲目</Empty>
          ) : null}
          {tracks.map((track, index) => {
            const currentTrack =
              !playback?.batchID && playback?.track.id === track.id;
            const trackPlaying = currentTrack && playing;
            return (
              <Box
                key={track.id}
                className="album-track-row"
                data-current={currentTrack}
                data-playing={trackPlaying}
              >
                <ButtonBase
                  className="album-track-play"
                  aria-label={
                    (currentTrack
                      ? playing
                        ? "暂停 "
                        : "继续播放 "
                      : "播放 ") + (track.tags.title || filename(track.path))
                  }
                  aria-current={currentTrack ? "true" : undefined}
                  aria-pressed={trackPlaying}
                  onClick={() =>
                    currentTrack
                      ? togglePlayback()
                      : onPlay({ track, queue: tracks })
                  }
                >
                  <Typography
                    component="span"
                    variant="caption"
                    className="album-track-number"
                  >
                    {currentTrack ? (
                      <PlaybackNotes active={trackPlaying} />
                    ) : (
                      track.tags.track.split("/")[0] || index + 1
                    )}
                  </Typography>
                  <Box component="span" className="album-track-copy">
                    <Typography
                      component="span"
                      variant="body2"
                      noWrap
                      sx={{ display: "block" }}
                    >
                      {track.tags.title || filename(track.path)}
                    </Typography>
                    <Typography
                      component="span"
                      variant="caption"
                      color="text.secondary"
                      noWrap
                      sx={{ display: "block" }}
                    >
                      {track.tags.artist || "未知歌手"}
                    </Typography>
                  </Box>
                  <Box
                    component="span"
                    className="album-track-quality"
                    aria-label={`音质：${audioQuality(track)}`}
                    title={audioQuality(track)}
                  >
                    {audioQuality(track)}
                  </Box>
                </ButtonBase>
                {showEditActions ? (
                  <Box className="album-track-actions">
                    <IconAction
                      label={
                        "编辑 " + (track.tags.title || filename(track.path))
                      }
                      disabled={!track.editable}
                      onClick={() => onEdit([track])}
                    >
                      <SquarePen size={16} />
                    </IconAction>
                  </Box>
                ) : null}
              </Box>
            );
          })}
          {tracks.length > 0 && tracks.length < total ? (
            <Box sx={{ display: "flex", justifyContent: "center", py: 1 }}>
              <Button
                size="small"
                color="primary"
                disabled={loading}
                onClick={() => void loadMore()}
              >
                {loading ? "加载中…" : "加载更多曲目"}
              </Button>
            </Box>
          ) : null}
        </Box>
      </Collapse>
    </Box>
  );
}
