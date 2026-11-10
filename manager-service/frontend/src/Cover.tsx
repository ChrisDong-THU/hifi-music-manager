import { useState } from "react";
import { Box } from "@mui/material";
import { useLibraryState } from "./api";
import { libraryMediaURL } from "./http-client";

function useArtworkSource(path: string) {
  const { session, trackRevision } = useLibraryState();
  const separator = path.includes("?") ? "&" : "?";
  return libraryMediaURL(`${path}${separator}v=${trackRevision}`, session);
}

export function useCoverSource(id: string) {
  return useArtworkSource(`/api/tracks/${id}/cover`);
}

type CoverTarget = { id: string; albumKey?: never } | { id?: never; albumKey: string };

export function Cover({ id, albumKey, size = 50 }: CoverTarget & { size?: number }) {
  const [failedSource, setFailedSource] = useState("");
  const source = useArtworkSource(
    albumKey === undefined
      ? `/api/tracks/${id}/cover`
      : `/api/albums/cover?key=${encodeURIComponent(albumKey)}`,
  );
  const failed = failedSource === source;
  return (
    <Box
      sx={{
        width: size,
        height: size,
        flexShrink: 0,
        bgcolor: "#24262a",
        display: "grid",
        placeItems: "center",
        overflow: "hidden",
        borderRadius: 0.5,
      }}
    >
      <Box
        component="img"
        src={failed ? "/default-cover.svg" : source}
        alt=""
        onError={failed ? undefined : () => setFailedSource(source)}
        loading="lazy"
        sx={{ width: "100%", height: "100%", objectFit: "cover" }}
      />
    </Box>
  );
}
