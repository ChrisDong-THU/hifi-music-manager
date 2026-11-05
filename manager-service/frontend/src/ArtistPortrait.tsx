import { useState } from "react";
import { Box } from "@mui/material";
import { UserRound } from "lucide-react";

export function ArtistPortrait({
  image,
  size = 46,
  shape = "circle",
}: {
  image?: string;
  size?: number;
  shape?: "circle" | "square";
}) {
  const [failed, setFailed] = useState("");
  return (
    <Box
      component="span"
      sx={{
        display: "grid",
        placeItems: "center",
        width: size,
        height: size,
        flexShrink: 0,
        borderRadius: shape === "square" ? 0.5 : "50%",
        overflow: "hidden",
        color: "primary.main",
        background:
          shape === "square"
            ? "#24262a"
            : "linear-gradient(145deg, #ffffff12, #ffffff03)",
      }}
    >
      {image && failed !== image ? (
        <Box
          component="img"
          src={image}
          alt=""
          loading="lazy"
          onError={() => setFailed(image)}
          sx={{ width: "100%", height: "100%", objectFit: "cover" }}
        />
      ) : (
        <UserRound size={size * 0.5} />
      )}
    </Box>
  );
}
