import { useState } from "react";
import { Alert, Box, Button, TextField } from "@mui/material";
import { useAppActions } from "./api";
import { Cover } from "./Cover";
import { ImageUpload } from "./ImageUpload";
import { DeleteMusicButton } from "./DeleteMusicButton";
import { LoadError, Modal } from "./components";
import { useConditionalQuery } from "./useConditionalQuery";
import { albumLocationLabel, type Album } from "./types";
import { useProgressNotice } from "./ProgressNotice";

const albumFields = {
  album: "专辑",
  album_artist: "专辑歌手",
  genre: "流派",
  year: "年份",
};
type Field = keyof typeof albumFields;
type Values = Record<Field, string>;
type Detail = {
  tags: Values;
  mixed: Field[];
  count: number;
  editable: boolean;
  edit_token: string;
};

export function AlbumEditor({
  album,
  onClose,
}: {
  album: Album;
  onClose: () => void;
}) {
  const { enqueue, notify } = useAppActions();
  const resource = useConditionalQuery<Detail>(
    `albums/detail?key=${encodeURIComponent(album.key)}`,
  );
  const detail = resource.data;
  const [edits, setEdits] = useState<Partial<Values>>({});
  const [cover, setCover] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const location = albumLocationLabel(album.location);
  useProgressNotice(resource.loading, "读取专辑信息");

  async function save() {
    if (!detail || !detail.editable || busy) return;
    const patch = Object.fromEntries(
      (Object.keys(edits) as Field[])
        .filter(
          (key) => detail.mixed.includes(key) || edits[key] !== detail.tags[key],
        )
        .map((key) => [key, edits[key]]),
    );
    if (!cover && !Object.keys(patch).length) {
      notify("没有需要保存的修改");
      return;
    }
    const form = new FormData();
    form.set("key", album.key);
    form.set("patch", JSON.stringify(patch));
    if (cover) form.set("cover", cover);
    setBusy(true);
    setError("");
    try {
      await enqueue("albums/edit", form, { ifMatch: detail.edit_token });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="编辑专辑"
      open
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <DeleteMusicButton
            title="删除专辑"
            description={`将${location ? `目录「${location}」中` : ""}《${album.title || "未分类"}》的全部 ${detail?.count ?? album.count} 首曲目永久删除，确认后无法恢复。`}
            disabled={busy || !detail}
            onDelete={async () => {
              if (!detail) return;
              await enqueue(
                "albums/delete",
                { key: album.key },
                { ifMatch: detail.edit_token },
              );
              onClose();
            }}
          />
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            variant="contained"
            disabled={busy || !detail?.editable}
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
      {location ? (
        <Alert severity="info" sx={{ mb: 2 }}>
          专辑目录 · {location}
        </Alert>
      ) : null}
      {detail ? (
        <>
          {!detail.editable ? (
            <Alert severity="info" sx={{ mb: 2 }}>
              该专辑含暂不支持编辑的格式，目前支持 FLAC 和 MP3。
            </Alert>
          ) : null}
          <Box
            sx={{
              display: "flex",
              flexDirection: "column",
              gap: 3,
              py: 1,
            }}
          >
            <ImageUpload
              file={cover}
              onChange={setCover}
              onError={setError}
              label="选择专辑封面"
              disabled={!detail.editable || busy}
            >
              <Cover albumKey={album.key} size={160} />
            </ImageUpload>
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: {
                  mobile: "minmax(0,1fr)",
                  tablet: "repeat(2,minmax(0,1fr))",
                },
                gap: 2,
              }}
            >
              {(Object.keys(albumFields) as Field[]).map((key) => (
                <TextField
                  key={key}
                  label={albumFields[key]}
                  placeholder={
                    detail.mixed.includes(key) ? "多种值" : undefined
                  }
                  slotProps={
                    detail.mixed.includes(key)
                      ? { inputLabel: { shrink: true } }
                      : undefined
                  }
                  disabled={!detail.editable || busy}
                  value={edits[key] ?? detail.tags[key]}
                  onChange={(event) => {
                    const value = event.target.value;
                    setEdits((old) => ({ ...old, [key]: value }));
                  }}
                />
              ))}
            </Box>
          </Box>
        </>
      ) : null}
    </Modal>
  );
}
