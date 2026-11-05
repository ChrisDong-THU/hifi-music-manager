import { useEffect, useRef, useState, type ReactNode } from "react";
import { Box, ButtonBase, Typography } from "@mui/material";
import { ImageUp, Undo2 } from "lucide-react";

/** Shared cover/portrait picker. Keep previews as data URLs to respect the CSP. */
export function ImageUpload({
  file,
  onChange,
  onError,
  label,
  disabled,
  children,
  size = 160,
}: {
  file: File | null;
  onChange: (file: File | null) => void;
  onError: (message: string) => void;
  label: string;
  disabled?: boolean;
  children: ReactNode;
  size?: number;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [preview, setPreview] = useState("");
  useEffect(() => {
    setPreview("");
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => setPreview(String(reader.result));
    reader.onerror = () => onError("无法预览该图片，请重新选择");
    reader.readAsDataURL(file);
    return () => {
      reader.onload = null;
      reader.onerror = null;
      if (reader.readyState === FileReader.LOADING) reader.abort();
    };
  }, [file, onError]);
  const action = file ? "取消选图" : "上传图片";
  return (
    <Box
      sx={{
        position: "relative",
        width: size,
        height: size,
        alignSelf: "center",
      }}
    >
      <ButtonBase
        aria-label={action}
        disabled={disabled}
        onClick={() => {
          if (file) {
            onChange(null);
            onError("");
          } else input.current?.click();
        }}
        sx={{
          width: "100%",
          height: "100%",
          overflow: "hidden",
          borderRadius: 1,
          "&:hover .image-upload-overlay, &.Mui-focusVisible .image-upload-overlay":
            { opacity: 1 },
          "&.Mui-focusVisible": {
            outline: "2px solid",
            outlineColor: "primary.main",
            outlineOffset: 3,
          },
        }}
      >
        {preview ? (
          <Box
            component="img"
            src={preview}
            alt="新图片预览"
            sx={{ width: "100%", height: "100%", objectFit: "cover" }}
          />
        ) : (
          children
        )}
        {!disabled ? (
          <Box
            component="span"
            className="image-upload-overlay"
            sx={{
              position: "absolute",
              inset: 0,
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              justifyContent: "center",
              gap: 1,
              bgcolor: "#00000099",
              color: "#fff",
              opacity: 0,
              transition: "opacity 160ms ease",
              "@media (hover: none)": {
                opacity: 1,
                inset: "auto 0 0",
                flexDirection: "row",
                py: 1,
                gap: 0.75,
              },
              "@media (prefers-reduced-motion: reduce)": { transition: "none" },
            }}
          >
            {file ? <Undo2 size={20} /> : <ImageUp size={20} />}
            <Typography component="span" variant="body2" fontWeight={500}>
              {action}
            </Typography>
          </Box>
        ) : null}
      </ButtonBase>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png"
        aria-label={label}
        hidden
        disabled={disabled}
        onChange={(event) => {
          const next = event.target.files?.[0];
          event.target.value = "";
          if (!next) return;
          if (
            !["image/jpeg", "image/png"].includes(next.type) ||
            next.size > 10 * 1024 * 1024
          ) {
            onError("请选择不超过 10 MiB 的 JPG 或 PNG 图片");
            return;
          }
          onError("");
          onChange(next);
        }}
      />
    </Box>
  );
}
