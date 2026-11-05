import { useId, useState, type ReactNode } from "react";
import {
  Box,
  Button,
  ButtonBase,
  Collapse,
  Tooltip,
  Typography,
  useMediaQuery,
} from "@mui/material";
import { ChevronDown, SquarePen } from "lucide-react";
import { useLibraryPage } from "./useLibraryPage";
import { useProgressNotice } from "./ProgressNotice";
import { Empty, IconAction, LoadError } from "./components";
import { ArtistEditor, type ArtistProfile } from "./ArtistEditor";
import { ArtistPortrait } from "./ArtistPortrait";
import type { Album, Artist } from "./types";

type ArtistAlbumsProps = {
  artist: Artist;
  revision: number;
  sort: string;
  renderAlbums: (albums: Album[]) => ReactNode;
};

function ArtistAlbums({
  artist,
  revision,
  sort,
  renderAlbums,
  onEdit,
}: ArtistAlbumsProps & { onEdit: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const endpoint = `albums?album_artist=${encodeURIComponent(artist.name)}&sort=${sort}`;
  const {
    items: albums,
    total,
    loading,
    error,
    loaded,
    reload,
    loadMore,
  } = useLibraryPage("albums", endpoint, revision, expanded);
  useProgressNotice(expanded && loading, "加载歌手专辑", artist.name);
  const panelID = useId();
  const reduceMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const biography = artist.biography?.trim() || "";
  return (
    <Box className="library-artist">
      <Box className="library-artist-heading">
        <ButtonBase
          className="library-artist-toggle"
          aria-label={`${artist.name} ${artist.albums} 张专辑 · ${artist.tracks} 首`}
          aria-expanded={expanded}
          aria-controls={panelID}
          aria-busy={expanded && loading}
          onClick={() => setExpanded((value) => !value)}
        >
          <ArtistPortrait image={artist.image_url} size={44} />
          <Box component="span" sx={{ flex: 1, minWidth: 0 }}>
            <Typography
              component="span"
              variant="body1"
              fontWeight="bold"
              className="library-entry-title"
              noWrap
              sx={{ display: "block" }}
            >
              {artist.name}
            </Typography>
            <Tooltip
              title={
                biography ? (
                  <Box
                    component="span"
                    sx={{
                      display: "block",
                      maxHeight: 320,
                      overflowY: "auto",
                      whiteSpace: "pre-wrap",
                      overflowWrap: "anywhere",
                    }}
                  >
                    {biography}
                  </Box>
                ) : (
                  ""
                )
              }
              describeChild
              arrow
              placement="bottom-start"
              enterDelay={450}
              slotProps={{
                tooltip: {
                  sx: {
                    maxWidth: "min(480px, calc(100vw - 112px))",
                    fontSize: 14,
                    fontWeight: 400,
                    lineHeight: 1.6,
                    p: 1.5,
                  },
                },
                arrow: {
                  sx: {
                    color: "#21252933",
                    fontSize: 16,
                    left: "50% !important",
                    transform: "translateX(-50%) !important",
                    "&::before": { backdropFilter: "blur(10px)" },
                  },
                },
              }}
            >
              <Typography
                component="span"
                variant="body2"
                color="text.secondary"
                noWrap
                className="library-artist-biography library-entry-subtitle"
                sx={{
                  display: "block",
                  width: "fit-content",
                  maxWidth: "100%",
                }}
              >
                {biography || "暂无简介"}
              </Typography>
            </Tooltip>
          </Box>
          <Typography
            component="span"
            variant="caption"
            color="text.secondary"
            noWrap
            className="library-artist-count"
          >
            {artist.albums} 张专辑 · {artist.tracks} 首
          </Typography>
          <Box component="span" className="library-artist-chevron">
            <ChevronDown
              size={20}
              style={{ transform: expanded ? "rotate(180deg)" : "none" }}
            />
          </Box>
        </ButtonBase>
        <IconAction
          label={`编辑歌手 ${artist.name}`}
          tooltip="编辑歌手"
          onClick={onEdit}
        >
          <SquarePen size={16} />
        </IconAction>
      </Box>
      <Collapse
        in={expanded && (loaded || !!error)}
        timeout={reduceMotion ? 0 : { enter: 280, exit: 220 }}
        easing={{
          enter: "cubic-bezier(0.22, 1, 0.36, 1)",
          exit: "cubic-bezier(0.4, 0, 0.2, 1)",
        }}
      >
        <Box
          id={panelID}
          role="region"
          aria-label={`${artist.name}的专辑`}
          aria-busy={loading}
          className="library-artist-albums"
        >
          <LoadError error={error} retry={() => void reload()} />
          {renderAlbums(albums)}
          {!loading && !error && !albums.length ? (
            <Empty kind="albums">暂无专辑</Empty>
          ) : null}
          {albums.length < total && loaded ? (
            <Box sx={{ display: "flex", justifyContent: "center", py: 1 }}>
              <Button
                color="primary"
                disabled={loading}
                onClick={() => void loadMore()}
              >
                {loading ? "加载中…" : "加载更多专辑"}
              </Button>
            </Box>
          ) : null}
        </Box>
      </Collapse>
    </Box>
  );
}

export function LibraryArtistsList({
  artists,
  loading,
  revision,
  sort,
  renderAlbums,
  onArtistSaved,
}: Omit<ArtistAlbumsProps, "artist"> & {
  artists: Artist[];
  loading: boolean;
  onArtistSaved: (profile: ArtistProfile) => void;
}) {
  const [editing, setEditing] = useState<Artist | null>(null);
  return (
    <>
      <Box>
        {artists.map((artist) => (
          <ArtistAlbums
            key={artist.name}
            artist={artist}
            revision={revision}
            sort={sort}
            renderAlbums={renderAlbums}
            onEdit={() => setEditing(artist)}
          />
        ))}
        {!artists.length && !loading ? (
          <Empty kind="artists">暂无歌手</Empty>
        ) : null}
      </Box>
      {editing ? (
        <ArtistEditor
          key={editing.name}
          artist={editing}
          onClose={() => setEditing(null)}
          onSaved={onArtistSaved}
        />
      ) : null}
    </>
  );
}
