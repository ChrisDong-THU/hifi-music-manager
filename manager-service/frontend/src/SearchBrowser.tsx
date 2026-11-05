import { useState, type ReactNode } from "react";
import { alpha, lighten } from "@mui/material/styles";
import {
  Box,
  Button,
  ButtonBase,
  Card,
  Chip,
  InputAdornment,
  TextField,
  Typography,
} from "@mui/material";
import {
  AudioLines,
  ChevronRight,
  Disc3,
  Info,
  Play,
  Search,
  SquarePen,
  UserRound,
  X,
} from "lucide-react";
import type { Album, Artist, Track } from "./types";
import { audioQuality, duration, filename } from "./types";
import type { Playback } from "./types";
import { Cover } from "./Cover";
import { HiResBadge } from "./HiResBadge";
import { Empty, IconAction, LoadError } from "./components";
import { ArtistPortrait } from "./ArtistPortrait";

export type SearchStage = "artists" | "albums" | "tracks" | "details";
const stages = [
  { id: "artists", title: "歌手", icon: <UserRound size={18} /> },
  { id: "albums", title: "专辑", icon: <Disc3 size={18} /> },
  { id: "tracks", title: "曲目", icon: <AudioLines size={18} /> },
  { id: "details", title: "详情", icon: <Info size={18} /> },
] as const;

function SearchPane({
  stage,
  active,
  count,
  children,
  footer,
}: {
  stage: SearchStage;
  active: SearchStage;
  count?: number;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const heading = stages.find((item) => item.id === stage)!;
  return (
    <Card
      component="section"
      aria-label={heading.title}
      className={`search-pane search-pane-${stage}`}
      data-active={active === stage}
    >
      <Box className="search-pane-header">
        <Box className="search-pane-icon">{heading.icon}</Box>
        <Typography component="h2" variant="body2" fontWeight={500}>
          {heading.title}
        </Typography>
        {count !== undefined ? (
          <Typography variant="caption" className="search-pane-count">
            {count}
          </Typography>
        ) : null}
      </Box>
      <Box className="search-pane-body">{children}</Box>
      {footer ? <Box className="search-pane-footer">{footer}</Box> : null}
    </Card>
  );
}

export function SearchBrowser({
  query,
  onQuery,
  artists,
  albums,
  tracks,
  total,
  albumTotal,
  artistFilter,
  albumFilter,
  details,
  stage,
  onStage,
  onArtist,
  onAlbum,
  onAllAlbums,
  onTrack,
  onPlay,
  onEdit,
  onLoadMore,
  loading,
  error,
  onRetry,
}: {
  query: string;
  onQuery: (query: string) => void;
  artists: Artist[];
  albums: Album[];
  tracks: Track[];
  total: number;
  albumTotal: number;
  artistFilter: string;
  albumFilter: string;
  details: Track | null;
  stage: SearchStage;
  onStage: (stage: SearchStage) => void;
  onArtist: (name: string) => void;
  onAlbum: (album: Album) => void;
  onAllAlbums: () => void;
  onTrack: (track: Track) => void;
  onPlay: (playback: Playback) => void;
  onEdit: (track: Track) => void;
  onLoadMore: (kind: "tracks" | "albums") => void;
  loading: boolean;
  error: string;
  onRetry: () => void;
}) {
  const [focusPulse, setFocusPulse] = useState(0);
  const activeAlbum = albums.find((album) => album.key === albumFilter);
  return (
    <Box
      className="search-workspace"
      sx={(theme) => ({
        "--search-accent": theme.palette.secondary.main,
        "--search-accent-light": theme.palette.secondary.light,
        "--search-accent-soft": alpha(theme.palette.secondary.main, 0.1),
        "--search-sweep-tint": alpha(theme.palette.secondary.main, 0.12),
        "--search-hover": alpha(theme.palette.secondary.main, 0.045),
        "--search-input": lighten(theme.palette.background.paper, 0.025),
        "--search-muted": theme.palette.text.secondary,
        "--search-faint": alpha(theme.palette.text.secondary, 0.72),
        "--search-icon-muted": alpha(theme.palette.secondary.light, 0.65),
        "& .search-album-row .hires-badge": {
          backgroundColor: theme.palette.secondary.main,
          color: theme.palette.secondary.contrastText,
        },
      })}
    >
      <LoadError error={error} retry={onRetry} />
      <Box className="search-toolbar">
        <Box className="search-query-shell">
          <TextField
            className="search-query-field"
            autoFocus
            size="medium"
            placeholder="搜索曲库"
            value={query}
            onChange={(event) => onQuery(event.target.value)}
            onFocus={(event) => {
              if (event.target instanceof HTMLInputElement)
                setFocusPulse((pulse) => pulse + 1);
            }}
            onPointerDown={(event) => {
              // Focus starts the first sweep; subsequent clicks restart it.
              if (
                event.target instanceof HTMLInputElement &&
                event.target === event.target.ownerDocument.activeElement
              )
                setFocusPulse((pulse) => pulse + 1);
            }}
            slotProps={{
              htmlInput: { "aria-label": "搜索曲库" },
              input: {
                startAdornment: (
                  <InputAdornment position="start">
                    <Box
                      component="span"
                      className="search-query-icon"
                      key={focusPulse}
                    >
                      <Search size={21} />
                    </Box>
                  </InputAdornment>
                ),
                endAdornment: query ? (
                  <InputAdornment position="end">
                    <IconAction label="清空搜索" onClick={() => onQuery("")}>
                      <X size={18} />
                    </IconAction>
                  </InputAdornment>
                ) : undefined,
              },
            }}
          />
          {focusPulse > 0 ? (
            <Box
              key={focusPulse}
              className="search-query-sweep"
              aria-hidden="true"
            />
          ) : null}
        </Box>
        {artistFilter || albumFilter ? (
          <Box className="search-filters" aria-label="当前筛选">
            {artistFilter ? (
              <Chip
                size="small"
                icon={<UserRound size={14} />}
                label={artistFilter}
                onDelete={() => onArtist("")}
              />
            ) : null}
            {albumFilter ? (
              <Chip
                size="small"
                icon={<Disc3 size={14} />}
                label={activeAlbum?.title || "已选专辑"}
                onDelete={onAllAlbums}
              />
            ) : null}
          </Box>
        ) : null}
      </Box>
      <Box className="search-stage-nav" role="group" aria-label="搜索分类">
        {stages.map((item) => (
          <ButtonBase
            key={item.id}
            aria-pressed={stage === item.id}
            onClick={() => onStage(item.id)}
          >
            {item.icon}
            <span>{item.title}</span>
          </ButtonBase>
        ))}
      </Box>
      <Box className="search-panes" aria-busy={loading}>
        <SearchPane stage="artists" active={stage} count={artists.length}>
          <ButtonBase
            className="search-all-row"
            data-selected={!artistFilter}
            onClick={() => onArtist("")}
          >
            <UserRound size={16} />
            <span>全部歌手</span>
            <ChevronRight size={15} />
          </ButtonBase>
          {artists.map((artist) => (
            <ButtonBase
              key={artist.name}
              className="search-result search-artist-row"
              data-selected={artistFilter === artist.name}
              aria-label={artist.name}
              onClick={() => onArtist(artist.name)}
            >
              <ArtistPortrait image={artist.image_url} size={36} />
              <Box component="span" className="search-result-copy">
                <Typography component="span" variant="body2" noWrap>
                  {artist.name}
                </Typography>
                <Typography
                  component="span"
                  variant="caption"
                  color="text.secondary"
                >
                  {artist.albums} 张专辑 · {artist.tracks} 首
                </Typography>
              </Box>
              <ChevronRight size={14} className="search-row-arrow" />
            </ButtonBase>
          ))}
          {!loading && !artists.length ? (
            <Empty kind="artists" fill>
              {query.trim() ? "未找到匹配的歌手" : "暂无歌手"}
            </Empty>
          ) : null}
        </SearchPane>
        <SearchPane stage="albums" active={stage} count={albumTotal}>
          <ButtonBase
            className="search-all-row"
            data-selected={!albumFilter}
            onClick={onAllAlbums}
          >
            <Disc3 size={16} />
            <span>全部专辑</span>
            <ChevronRight size={15} />
          </ButtonBase>
          {albums.map((album) => (
            <ButtonBase
              key={album.key}
              className="search-result search-album-row"
              data-selected={albumFilter === album.key}
              aria-label={`${album.title || "未分类"} ${album.artist || "未知歌手"}`}
              onClick={() => onAlbum(album)}
            >
              <Cover id={album.cover_id} size={42} />
              <Box component="span" className="search-result-copy">
                <Box component="span" className="album-title-line">
                  <Typography component="span" variant="body2" noWrap>
                    {album.title || "未分类"}
                  </Typography>
                  {album.hi_res ? <HiResBadge /> : null}
                </Box>
                <Typography
                  component="span"
                  variant="caption"
                  color="text.secondary"
                  noWrap
                >
                  {album.artist || "未知歌手"}
                  {album.year ? ` · ${album.year}` : ""}
                </Typography>
              </Box>
              <ChevronRight size={14} className="search-row-arrow" />
            </ButtonBase>
          ))}
          {!loading && !albums.length ? (
            <Empty kind="albums" fill>
              {query.trim() ? "未找到匹配的专辑" : "暂无专辑"}
            </Empty>
          ) : null}
          {albums.length < albumTotal ? (
            <Button
              className="search-load-more"
              color="secondary"
              disabled={loading}
              onClick={() => onLoadMore("albums")}
            >
              加载更多
            </Button>
          ) : null}
        </SearchPane>
        <SearchPane stage="tracks" active={stage} count={total}>
          {tracks.map((track, index) => (
            <Box
              key={track.id}
              className="search-track-row"
              data-selected={details?.id === track.id}
            >
              <ButtonBase
                className="search-track-select"
                aria-label={`查看 ${track.tags.title || filename(track.path)}`}
                onClick={() => onTrack(track)}
                onDoubleClick={() => onPlay({ track, queue: tracks })}
              >
                <Typography
                  component="span"
                  variant="caption"
                  className="search-track-index"
                >
                  {index + 1}
                </Typography>
                <Box component="span" className="search-result-copy">
                  <Typography component="span" variant="body2" noWrap>
                    {track.tags.title || filename(track.path)}
                  </Typography>
                  <Typography
                    component="span"
                    variant="caption"
                    color="text.secondary"
                    noWrap
                  >
                    {track.tags.artist || "未知歌手"}
                  </Typography>
                </Box>
                <Typography
                  component="span"
                  variant="caption"
                  className="search-track-duration"
                >
                  {duration(track.duration)}
                </Typography>
              </ButtonBase>
              <IconAction
                label={`播放 ${track.tags.title || filename(track.path)}`}
                onClick={() => onPlay({ track, queue: tracks })}
              >
                <Play size={16} />
              </IconAction>
            </Box>
          ))}
          {!loading && !tracks.length ? (
            <Empty kind="tracks" fill>
              {query.trim() ? "未找到匹配的曲目" : "暂无曲目"}
            </Empty>
          ) : null}
          {tracks.length < total ? (
            <Button
              className="search-load-more"
              color="secondary"
              disabled={loading}
              onClick={() => onLoadMore("tracks")}
            >
              加载更多
            </Button>
          ) : null}
        </SearchPane>
        <SearchPane
          stage="details"
          active={stage}
          footer={
            details ? (
              <Box className="search-detail-actions">
                <IconAction
                  label="编辑标签"
                  disabled={!details.editable}
                  onClick={() => onEdit(details)}
                >
                  <SquarePen size={18} />
                </IconAction>
              </Box>
            ) : null
          }
        >
          {details ? (
            <Box className="search-track-detail">
              <Box className="search-detail-art">
                <Cover id={details.id} size={112} />
              </Box>
              <Typography component="h3" className="search-detail-title">
                {details.tags.title || filename(details.path)}
              </Typography>
              <Typography
                variant="body2"
                color="text.secondary"
                className="search-detail-artist"
              >
                {details.tags.artist || "未知歌手"}
              </Typography>
              <Box className="search-detail-badges">
                <Chip size="small" label={audioQuality(details)} />
                <Chip size="small" label={duration(details.duration)} />
              </Box>
              <Box component="dl" className="search-detail-metadata">
                {[
                  ["专辑", details.tags.album],
                  [
                    "专辑歌手",
                    details.tags.album_artist !== details.tags.artist
                      ? details.tags.album_artist
                      : "",
                  ],
                  ["年份", details.tags.year],
                  ["曲序", details.tags.track],
                  ["碟号", details.tags.disc],
                  ["流派", details.tags.genre],
                ]
                  .filter(([, value]) => value)
                  .map(([name, value]) => (
                    <Box
                      key={name}
                      className={
                        name === "专辑" || name === "专辑歌手"
                          ? "search-meta-wide"
                          : undefined
                      }
                    >
                      <Typography
                        component="dt"
                        variant="caption"
                        color="text.secondary"
                      >
                        {name}
                      </Typography>
                      <Typography component="dd" variant="body2">
                        {value}
                      </Typography>
                    </Box>
                  ))}
              </Box>
            </Box>
          ) : (
            <Empty kind="details" fill>
              选择曲目查看详情
            </Empty>
          )}
        </SearchPane>
      </Box>
    </Box>
  );
}
