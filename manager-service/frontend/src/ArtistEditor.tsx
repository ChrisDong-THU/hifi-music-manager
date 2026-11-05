import { useState } from "react";
import { Box, Button, TextField } from "@mui/material";
import { useAppActions } from "./api";
import { ArtistPortrait } from "./ArtistPortrait";
import { ImageUpload } from "./ImageUpload";
import { LoadError, Modal } from "./components";
import { useConditionalQuery } from "./useConditionalQuery";
import type { Artist } from "./types";
import { useProgressNotice } from "./ProgressNotice";
import { useNotifications } from "./Notifications";

export type ArtistProfile = Pick<Artist, "name" | "biography" | "image_url">;

export function ArtistEditor({
  artist,
  onClose,
  onSaved,
}: {
  artist: Artist;
  onClose: () => void;
  onSaved: (profile: ArtistProfile) => void;
}) {
  const { request, notify } = useAppActions();
  const resource = useConditionalQuery<ArtistProfile>(
    `artists/detail?name=${encodeURIComponent(artist.name)}`,
  );
  const detail = resource.data;
  const [biography, setBiography] = useState<string | null>(null);
  const [picture, setPicture] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const notices = useNotifications();
  useProgressNotice(resource.loading, "读取歌手信息");

  async function save() {
    if (!detail || busy) return;
    const text = biography ?? detail.biography ?? "";
    if (!picture && text.trim() === (detail.biography || "")) {
      notify("没有需要保存的修改");
      return;
    }
    const form = new FormData();
    form.set("name", artist.name);
    form.set("biography", text);
    if (picture) form.set("picture", picture);
    setBusy(true);
    setError("");
    const noticeID = notices.begin("保存歌手信息", artist.name);
    try {
      const profile = await request<ArtistProfile>("artists/edit", form);
      onSaved(profile);
      notices.update(noticeID, { label: "歌手信息已保存", status: "success" });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
      notices.update(noticeID, {
        label: e instanceof Error ? e.message : "保存失败",
        status: "error",
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="编辑歌手"
      open
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            variant="contained"
            disabled={busy || !detail}
            onClick={() => void save()}
          >
            保存
          </Button>
        </>
      }
    >
      <LoadError
        error={error || resource.error}
        retry={detail ? undefined : resource.refresh}
      />
      {detail ? (
        <Box
          sx={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 3,
            py: 1,
          }}
        >
          <ImageUpload
            file={picture}
            onChange={setPicture}
            onError={setError}
            label="选择歌手图片"
            disabled={busy}
          >
            <ArtistPortrait
              image={detail.image_url}
              size={160}
              shape="square"
            />
          </ImageUpload>
          <TextField
            label="歌手简介"
            multiline
            minRows={3}
            disabled={busy}
            value={biography ?? detail.biography ?? ""}
            onChange={(event) => setBiography(event.target.value)}
            slotProps={{
              htmlInput: { maxLength: 5000 },
            }}
          />
        </Box>
      ) : null}
    </Modal>
  );
}
