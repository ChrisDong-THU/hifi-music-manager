import { memo, useDeferredValue, useId, useState } from "react";
import {
  Box,
  Button,
  ButtonBase,
  Card,
  MenuItem,
  TextField,
  Tooltip,
} from "@mui/material";
import { Disc3, RefreshCw, Settings, UsersRound } from "lucide-react";
import { useAppActions, useLibraryState } from "./api";
import { ScrollableCardContent } from "./ScrollableCardContent";
import type { Album, Artist, Track, Playback } from "./types";
import { Empty, IconAction, Modal, PageTitle, LoadError } from "./components";
import { DirectoryHeader } from "./upstream/primitives";
import { SearchBrowser, type SearchStage } from "./SearchBrowser";
import { LibraryArtistsList } from "./LibraryArtists";
import { AlbumEditor } from "./AlbumEditor";
import { AlbumRow } from "./AlbumRow";
import { TrackEditor } from "./TrackEditor";
import { useProgressNotice } from "./ProgressNotice";
import { useLibraryPage } from "./useLibraryPage";
import { useConditionalQuery } from "./useConditionalQuery";

const librarySortModes = [
  { value: "recent", label: "最近加入" },
  { value: "title", label: "专辑名称" },
  { value: "artist", label: "歌手名称" },
  { value: "year", label: "发行年份（从新到旧）" },
] as const;
type LibrarySortMode = (typeof librarySortModes)[number]["value"];
const libraryPreferencesKey = "hifi-library-preferences";
function readLibrarySortMode(): LibrarySortMode {
  try {
    const saved = JSON.parse(
      localStorage.getItem(libraryPreferencesKey) || "{}",
    );
    return (
      librarySortModes.find((mode) => mode.value === saved?.sortMode)?.value ||
      "recent"
    );
  } catch {
    return "recent";
  }
}

export const LibraryPage = memo(function LibraryPage({
  search = false,
  onPlay,
}: {
  search?: boolean;
  onPlay: (value: Playback) => void;
}) {
  const {
    trackRevision: revision,
    resourceRevision,
    musicPath,
    ready,
    scanning,
  } = useLibraryState();
  const { enqueue, notify } = useAppActions();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [albumEdit, setAlbumEdit] = useState<Album | null>(null);
  const [sortMode, setSortMode] = useState(readLibrarySortMode);
  const [grouping, setGrouping] = useState<"albums" | "artists">("albums");
  const listID = useId();
  const [query, setQuery] = useState(""),
    deferredQuery = useDeferredValue(query),
    [edit, setEdit] = useState<Track[] | null>(null),
    [detailID, setDetailID] = useState<string | null>(null),
    [artistFilter, setArtistFilter] = useState(""),
    [albumFilter, setAlbumFilter] = useState(""),
    [stage, setStage] = useState<SearchStage>("artists");
  const albumSort = search ? "title" : sortMode;

  function updateSortMode(mode: LibrarySortMode) {
    setSortMode(mode);
    try {
      localStorage.setItem(
        libraryPreferencesKey,
        JSON.stringify({ sortMode: mode }),
      );
    } catch {
      notify("曲库设置已生效，但无法在当前浏览器中保存", true);
    }
  }
  const q = encodeURIComponent(deferredQuery);
  const artist = encodeURIComponent(artistFilter);
  const trackPage = useLibraryPage(
    "tracks",
    `tracks?q=${q}&artist=${artist}&album=${encodeURIComponent(albumFilter)}&limit=100`,
    revision,
    search,
  );
  const albumPage = useLibraryPage(
    "albums",
    `albums?q=${q}&artist=${artist}&sort=${albumSort}`,
    revision,
    search || grouping === "albums",
  );
  const { items: tracks, total } = trackPage;
  const { items: albums, total: albumTotal } = albumPage;
  const showArtists = search || grouping === "artists";
  const artistQuery = useConditionalQuery<Artist[]>(
    `artists?q=${q}`,
    `${revision}:${resourceRevision}`,
    showArtists && ready,
  );
  const artists = artistQuery.data ?? [];
  const loading = search
    ? trackPage.loading || albumPage.loading || artistQuery.loading
    : grouping === "albums"
      ? albumPage.loading
      : artistQuery.loading;
  const error = search
    ? trackPage.error || albumPage.error || artistQuery.error
    : grouping === "albums"
      ? albumPage.error
      : artistQuery.error;
  function retry() {
    if (search) void trackPage.reload();
    if (search || grouping === "albums") void albumPage.reload();
    if (showArtists) void artistQuery.refresh();
  }
  const details = tracks.find((track) => track.id === detailID) || null;
  useProgressNotice(loading, search ? "搜索曲库" : "加载曲库");
  const chooseAlbum = (album: Album) => {
    setAlbumFilter(album.key);
    setDetailID(null);
    setStage("tracks");
  };
  const loadMore = (kind: "tracks" | "albums") =>
    kind === "tracks" ? trackPage.loadMore() : albumPage.loadMore();
  function inlineAlbumRows(list: Album[], showEditActions = true) {
    return list.map((album) => (
      <AlbumRow
        key={album.key}
        album={album}
        revision={revision}
        onPlay={onPlay}
        onEdit={setEdit}
        onAlbumEdit={setAlbumEdit}
        showEditActions={showEditActions}
      />
    ));
  }
  if (search)
    return (
      <>
        <PageTitle />
        <SearchBrowser
          query={query}
          onQuery={(value) => {
            setQuery(value);
            setArtistFilter("");
            setAlbumFilter("");
            setDetailID(null);
            setStage("tracks");
          }}
          artists={artists}
          albums={albums}
          tracks={tracks}
          total={total}
          albumTotal={albumTotal}
          artistFilter={artistFilter}
          albumFilter={albumFilter}
          details={details}
          stage={stage}
          onStage={setStage}
          onArtist={(name) => {
            setArtistFilter(name);
            setAlbumFilter("");
            setDetailID(null);
            setStage("albums");
          }}
          onAlbum={chooseAlbum}
          onAllAlbums={() => {
            setAlbumFilter("");
            setDetailID(null);
            setStage("tracks");
          }}
          onTrack={(track) => {
            setDetailID(track.id);
            setStage("details");
          }}
          onPlay={onPlay}
          onEdit={(track) => setEdit([track])}
          onLoadMore={(kind) => void loadMore(kind)}
          loading={loading}
          error={error}
          onRetry={retry}
        />
        {edit ? (
          <TrackEditor tracks={edit} onClose={() => setEdit(null)} />
        ) : null}
      </>
    );
  return (
    <>
      <PageTitle
        left={
          <IconAction
            label="重新扫描曲库"
            disabled={!ready || scanning}
            onClick={() =>
              void enqueue("scan").catch((error) =>
                notify(
                  error instanceof Error ? error.message : "扫描失败",
                  true,
                ),
              )
            }
          >
            <RefreshCw size={20} />
          </IconAction>
        }
        right={
          <IconAction label="曲库设置" onClick={() => setSettingsOpen(true)}>
            <Settings size={20} />
          </IconAction>
        }
      />
      {error ? (
        <Box sx={{ mb: 2 }}>
          <LoadError error={error} retry={retry} />
        </Box>
      ) : null}
      <Card
        className="library-panel scroll-panel"
        component="section"
        aria-label="曲库"
        sx={{
          p: { mobile: 1, tablet: 2 },
          width: "100%",
        }}
      >
        <Box className="panel-header">
          <DirectoryHeader
            icon={
              grouping === "albums" ? (
                <Disc3 size={24} />
              ) : (
                <UsersRound size={24} />
              )
            }
            path={musicPath}
            actions={
              <Tooltip
                title={
                  grouping === "albums"
                    ? "切换为按歌手整理"
                    : "切换为按专辑整理"
                }
              >
                <ButtonBase
                  className="library-view-switch"
                  disableRipple
                  aria-label={
                    grouping === "albums"
                      ? "切换为按歌手整理"
                      : "切换为按专辑整理"
                  }
                  aria-controls={`${listID}-albums ${listID}-artists`}
                  onClick={() =>
                    setGrouping((current) =>
                      current === "albums" ? "artists" : "albums",
                    )
                  }
                >
                  <span>{grouping === "albums" ? "Albums" : "Artists"}</span>
                </ButtonBase>
              </Tooltip>
            }
          />
        </Box>
        <ScrollableCardContent
          className="library-panel-scroll"
          id={`${listID}-albums`}
          hidden={grouping !== "albums"}
          label="专辑列表"
        >
          <Box
            id="library-album-list"
            sx={{
              display: "grid",
              gridTemplateColumns: "minmax(0,1fr)",
            }}
          >
            {albums.length ? (
              inlineAlbumRows(albums)
            ) : !loading ? (
              <Empty kind="albums">暂无专辑</Empty>
            ) : null}
          </Box>
          {albums.length < albumTotal ? (
            <Box sx={{ pt: 2, display: "flex", justifyContent: "center" }}>
              <Button
                color="primary"
                disabled={loading}
                onClick={() => void loadMore("albums")}
              >
                {loading ? "加载中…" : "加载更多"}
              </Button>
            </Box>
          ) : null}
        </ScrollableCardContent>
        <ScrollableCardContent
          className="library-panel-scroll"
          id={`${listID}-artists`}
          hidden={grouping !== "artists"}
          label="歌手列表"
        >
          <LibraryArtistsList
            artists={artists}
            loading={loading}
            revision={revision}
            sort={albumSort}
            renderAlbums={(list) => inlineAlbumRows(list, false)}
            onArtistSaved={() => void artistQuery.refresh()}
          />
        </ScrollableCardContent>
      </Card>
      <Modal
        title="曲库设置"
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        actions={
          <Button color="secondary" onClick={() => setSettingsOpen(false)}>
            完成
          </Button>
        }
      >
        <TextField
          select
          label="排序模式"
          value={sortMode}
          onChange={(e) => {
            const mode = librarySortModes.find(
              (mode) => mode.value === e.target.value,
            );
            if (mode) updateSortMode(mode.value);
          }}
        >
          {librarySortModes.map((mode) => (
            <MenuItem key={mode.value} value={mode.value}>
              {mode.label}
            </MenuItem>
          ))}
        </TextField>
      </Modal>
      {edit ? (
        <TrackEditor tracks={edit} onClose={() => setEdit(null)} />
      ) : null}
      {albumEdit ? (
        <AlbumEditor
          key={albumEdit.key}
          album={albumEdit}
          onClose={() => setAlbumEdit(null)}
        />
      ) : null}
    </>
  );
});
