import { useEffect, useState } from "react";
import { Alert, Box, Button, Typography } from "@mui/material";
import { LoadError } from "./components";
import { albumLocationLabel, type Tags } from "./types";
import { useConditionalQuery } from "./useConditionalQuery";

export const separateAlbumMatch = "new";
type AlbumIdentity = { title: string; artist: string; year: string };
type AlbumLookup = AlbumIdentity & { mixedYears: boolean; completeYear: boolean };
export type AlbumMatch = AlbumIdentity & {
  key: string;
  count: number;
  location: string;
  year_conflict?: boolean;
};

export function editionYear(value: string): string {
  const year = value.trim();
  const date = /^(\d{4})[-/.]/.exec(year);
  return date && date[1] !== "0000" ? date[1] : year;
}

export function albumIdentity(tags: Tags[]): AlbumLookup | null {
  const first = tags[0];
  if (!first) return null;
  const title = first.album.trim();
  const artistOf = (tag: Tags) => tag.album_artist.trim() || tag.artist.trim();
  const artist = artistOf(first);
  if (!title || !artist) return null;
  let year = "";
  let mixedYears = false;
  let allDated = true;
  for (const tag of tags) {
    if (tag.album.trim() !== title || artistOf(tag) !== artist) return null;
    const nextYear = editionYear(tag.year);
    if (!nextYear) {
      allDated = false;
      continue;
    }
    if (year && year !== nextYear) mixedYears = true;
    year = nextYear;
  }
  return {
    title,
    artist,
    year: mixedYears ? "" : year,
    mixedYears,
    completeYear: allDated && !mixedYears && /^\d{4}$/.test(year) && year !== "0000",
  };
}

export function applyAlbumIdentity(tag: Tags, album: AlbumIdentity): Tags {
  return {
    ...tag,
    album: album.title,
    album_artist: album.artist,
    year: !album.year || editionYear(tag.year) === album.year ? tag.year : album.year,
  };
}

export function DraftAlbumMatches({
  identity,
  revision,
  selection,
  disabled,
  canApply,
  onSelect,
  onSelectionChange,
}: {
  identity: AlbumLookup | null;
  revision: number;
  selection: string;
  disabled: boolean;
  canApply: (album: AlbumMatch) => boolean;
  onSelect: (album: AlbumMatch) => void;
  onSelectionChange: (key: "" | typeof separateAlbumMatch) => void;
}) {
  const path = identity
    ? "albums/matches?" + new URLSearchParams({
        title: identity.title,
        artist: identity.artist,
        year: identity.year,
      }).toString()
    : "";
  const [debounced, setDebounced] = useState(path);
  useEffect(() => {
    if (path === debounced) return;
    const timer = window.setTimeout(() => setDebounced(path), 250);
    return () => window.clearTimeout(timer);
  }, [debounced, path]);
  const ready = !!path && path === debounced;
  const matches = useConditionalQuery<{
    matches: AlbumMatch[];
    requires_choice: boolean;
  }>(debounced, revision, ready);
  const result = ready && !matches.error ? matches.data : null;
  const choices = (result?.matches ?? []).map((album) => ({
    album,
    applicable: canApply(album),
  }));
  const current = choices.some(({ album }) => album.key === selection);
  const separate = selection === separateAlbumMatch;
  const canSelectSeparate = choices.length > 0 && identity?.completeYear;
  const needsChoice =
    !!choices.length &&
    (result?.requires_choice || identity?.mixedYears) &&
    !current &&
    !separate;
  const stale = !!(result && selection && !current && !separate);
  const pending = !!identity && (!ready || matches.loading) && !result;
  let guidance = "";
  if (!identity)
    guidance = "填写一致的专辑名和专辑歌手，即可为这一组曲目选择曲库中的目标专辑。";
  else if (pending) guidance = "正在查找曲库中的同名专辑…";
  else if (stale) guidance = "已选专辑不再匹配，请重新选择或清除手动选择。";
  else if (needsChoice)
    guidance = "曲库中的位置或年份有差异。请在导入前确认如何放置这张专辑。";
  else if (choices.length === 1 && !selection)
    guidance = "导入时会沿用此专辑的目录。选择后可将现有专辑信息填入草稿。";
  else if (result && !choices.length)
    guidance = "曲库中没有同名且同歌手的专辑。";
  return (
    <Box
      component="section"
      aria-label="曲库专辑匹配"
      sx={{ mt: 2, display: "flex", flexDirection: "column", gap: 1 }}
    >
      <Typography variant="subtitle2">曲库专辑匹配</Typography>
      {ready && matches.error ? (
        <LoadError error={matches.error} retry={() => void matches.refresh()} />
      ) : stale || needsChoice ? (
        <Alert severity="warning">{guidance}</Alert>
      ) : guidance ? (
        <Typography
          variant="caption"
          color="text.secondary"
          role={pending ? "status" : undefined}
        >
          {guidance}
        </Typography>
      ) : null}
      {choices.map(({ album, applicable }) => (
        <Box
          key={album.key}
          sx={{
            display: "flex",
            alignItems: "center",
            flexWrap: "wrap",
            justifyContent: "space-between",
            gap: 1,
            p: 1.5,
            border: "1px solid",
            borderColor: album.key === selection ? "secondary.main" : "divider",
            borderRadius: 1,
          }}
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="body2" sx={{ overflowWrap: "anywhere" }}>
              {album.artist} · 《{album.title}》
            </Typography>
            <Typography
              variant="caption"
              component="div"
              color="text.secondary"
              sx={{ overflowWrap: "anywhere" }}
            >
              {[
                album.year_conflict ? "年份不一致" : album.year,
                `${album.count} 首`,
                albumLocationLabel(album.location),
              ]
                .filter(Boolean)
                .join(" · ")}
            </Typography>
          </Box>
          {album.key === selection ? (
            <Typography variant="caption" color="secondary.light">
              已选择
            </Typography>
          ) : (
            <Button
              size="small"
              color="secondary"
              disabled={disabled || !applicable}
              onClick={() => onSelect(album)}
            >
              匹配此专辑
            </Button>
          )}
        </Box>
      ))}
      {choices.some(({ applicable }) => !applicable) ? (
        <Typography variant="caption" color="text.secondary">
          部分曲目的格式不支持修改标签，无法应用不一致的专辑信息。
        </Typography>
      ) : null}
      {separate ? (
        <Alert severity={identity?.completeYear ? "info" : "warning"}>
          {identity?.completeYear
            ? "已选择独立版本。导入时将使用草稿标签并存入单独目录。"
            : "已选择独立版本。请为这组曲目填写相同的发行年份后再保存。"}
        </Alert>
      ) : canSelectSeparate ? (
        <Box
          sx={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 1 }}
        >
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ flex: "1 1 240px" }}
          >
            如果这是另一发行版本，可使用草稿中的标签并导入单独的目录。
          </Typography>
          <Button
            size="small"
            color="secondary"
            disabled={disabled}
            onClick={() => onSelectionChange(separateAlbumMatch)}
          >
            设为独立版本
          </Button>
        </Box>
      ) : choices.length > 0 && identity ? (
        <Typography variant="caption" color="text.secondary">
          若要单独导入这一发行版本，请先为这组曲目填写相同的发行年份。
        </Typography>
      ) : null}
      {selection ? (
        <Button
          size="small"
          color="secondary"
          disabled={disabled}
          sx={{ alignSelf: "flex-start" }}
          onClick={() => onSelectionChange("")}
        >
          清除手动选择
        </Button>
      ) : null}
    </Box>
  );
}
