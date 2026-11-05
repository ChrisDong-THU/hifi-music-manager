import { useState } from "react";
import { Box } from "@mui/material";
import { useLibraryState } from "./api";
import { libraryMediaURL } from "./http-client";

export function useCoverSource(id: string) {
  const { session, trackRevision } = useLibraryState();
  return libraryMediaURL(`/api/tracks/${id}/cover?v=${trackRevision}`, session);
}

export function Cover({ id, size = 50 }: { id: string; size?: number }) {
  const [failedSource, setFailedSource] = useState("");
  const source = useCoverSource(id);
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
      {failedSource === source ? (
        <Box
          component="img"
          src="/default-cover.svg"
          alt=""
          sx={{ width: "100%", height: "100%", objectFit: "cover" }}
        />
      ) : (
        <Box
          component="img"
          src={source}
          alt=""
          onError={() => setFailedSource(source)}
          loading="lazy"
          sx={{ width: "100%", height: "100%", objectFit: "cover" }}
        />
      )}
    </Box>
  );
}
