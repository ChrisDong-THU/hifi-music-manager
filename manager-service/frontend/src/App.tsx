import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Box } from "@mui/material";
import { useAppActions, useAppState, useLibraryState } from "./api";
import { useRoute } from "./routing";
import { Player } from "./Player";
import { PlaybackContext } from "./playback";
import { HomePage } from "./Home";
import { InboxPage } from "./Inbox";
import { useUpload } from "./upload";
import { DraftPage } from "./DraftDetail";
import { useProgressNotice } from "./ProgressNotice";
import { LibraryPage } from "./Library";
import type { Playback } from "./types";
import { NavBar } from "./upstream/NavBar";
import { PageWrapper } from "./upstream/page";

export function App() {
  const { session } = useLibraryState();
  // Reset disk-specific views and audio while keeping the provider's shared
  // notifications alive throughout a disk switch.
  return <DiskApp key={session} />;
}

function DiskApp() {
  const { state, error } = useAppState();
  const { notify } = useAppActions();
  useProgressNotice(!state && !error, "加载资源");
  const { path, params } = useRoute();
  const upload = useUpload();
  const [playback, setPlayback] = useState<Playback | null>(null);
  const [playing, setPlaying] = useState(false);
  const audio = useRef<HTMLAudioElement>(null);
  const togglePlayback = useCallback(() => {
    const node = audio.current;
    if (!node) return;
    if (!node.paused) node.pause();
    else void node.play().catch(() => notify("无法播放此音频，请重试", true));
  }, [notify]);
  const playbackControls = useMemo(
    () => ({ playback, playing, audio, setPlaying, togglePlayback }),
    [playback, playing, togglePlayback],
  );
  const scroll = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (path === "history") {
      location.replace("#home");
      return;
    }
    scroll.current?.scrollTo({ top: 0 });
  }, [path, params.get("draft")]);
  let content;
  if (!state)
    content = (
      <Box sx={{ display: "grid", placeItems: "center", minHeight: "60dvh" }}>
        {error ? <Alert severity="error">{error}</Alert> : null}
      </Box>
    );
  else if (path === "home") content = <HomePage />;
  else if (path === "inbox" && params.has("draft"))
    content = (
      <DraftPage key={params.get("draft")} id={params.get("draft") || ""} />
    );
  else if (path === "inbox")
    content = <InboxPage onPlay={setPlayback} upload={upload} />;
  else if (path === "library") content = <LibraryPage onPlay={setPlayback} />;
  else if (path === "search")
    content = <LibraryPage key="search" search onPlay={setPlayback} />;
  else content = <HomePage />;
  return (
    <PlaybackContext.Provider value={playbackControls}>
      <Box
        component="main"
        data-player-open={Boolean(playback)}
        sx={{
          height: "100dvh",
          width: "100%",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {upload.controls}
        {upload.progressView}
        <NavBar path={path} />
        <Box
          ref={scroll}
          className="app-scroll"
          data-page={path}
          sx={(theme) => ({
            flex: 1,
            overflow: "auto",
            pb: playback ? { mobile: "170px", tablet: "206px" } : "88px",
            pt: 1,
            [theme.breakpoints.up("laptop")]: {
              pt: "64px",
              pb: playback ? "128px" : 3,
            },
          })}
        >
          <PageWrapper
            className={
              path === "library"
                ? "library-page"
                : path === "home"
                  ? "home-page"
                  : path === "search"
                    ? "search-page"
                    : path === "inbox" && !params.has("draft")
                      ? "inbox-page"
                      : undefined
            }
            sx={{ px: { mobile: 0.5, laptop: 1 }, minWidth: "0!important" }}
          >
            {error && state ? (
              <Alert severity="error" sx={{ mb: 1 }}>
                {error}
              </Alert>
            ) : null}
            {content}
          </PageWrapper>
        </Box>
        {playback ? (
          <Player
            playback={playback}
            onChange={setPlayback}
            onClose={() => setPlayback(null)}
          />
        ) : null}
      </Box>
    </PlaybackContext.Provider>
  );
}
