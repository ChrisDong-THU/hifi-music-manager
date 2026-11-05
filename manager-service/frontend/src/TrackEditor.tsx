import { useState } from "react";
import {
  Box,
  Button,
  Checkbox,
  FormControlLabel,
  TextField,
  Typography,
} from "@mui/material";
import { useAppActions } from "./api";
import { Modal } from "./components";
import { DeleteMusicButton } from "./DeleteMusicButton";
import { fields, filename, type Tags, type Track } from "./types";

export function TrackEditor({
  tracks,
  onClose,
}: {
  tracks: Track[];
  onClose: () => void;
}) {
  const { enqueue, notify } = useAppActions();
  const trackFields: (keyof Tags)[] = [
    "title",
    "artist",
    "track",
    "disc",
    "lyrics",
  ];
  const multi = tracks.length > 1;
  const [values, setValues] = useState<Tags>(
      () =>
        Object.fromEntries(
          Object.keys(fields).map((k) => [
            k,
            tracks.every(
              (t) =>
                t.tags[k as keyof Tags] === tracks[0].tags[k as keyof Tags],
            )
              ? tracks[0].tags[k as keyof Tags]
              : "",
          ]),
        ) as Tags,
    ),
    [applied, setApplied] = useState(new Set<keyof Tags>()),
    [busy, setBusy] = useState(false);
  return (
    <Modal
      title={multi ? "批量编辑 · " + tracks.length + " 首" : "编辑标签"}
      open
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <DeleteMusicButton
            title="删除曲目"
            description={`${multi ? `将所选 ${tracks.length} 首曲目` : `将《${tracks[0].tags.title || filename(tracks[0].path)}》`}永久删除，确认后无法恢复。`}
            disabled={busy || !tracks.length}
            onDelete={async () => {
              await enqueue("tracks/delete", {
                ids: tracks.map((track) => track.id),
              });
              onClose();
            }}
          />
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            variant="contained"
            disabled={busy}
            onClick={async () => {
              const patch = Object.fromEntries(
                trackFields
                  .filter((k) =>
                    multi ? applied.has(k) : values[k] !== tracks[0].tags[k],
                  )
                  .map((k) => [k, values[k]]),
              );
              if (!Object.keys(patch).length) {
                notify("没有需要保存的修改");
                return;
              }
              setBusy(true);
              try {
                await enqueue("tracks/edit", {
                  ids: tracks.map((t) => t.id),
                  patch,
                });
                onClose();
              } catch (e) {
                notify(e instanceof Error ? e.message : "保存失败", true);
              } finally {
                setBusy(false);
              }
            }}
          >
            保存
          </Button>
        </>
      }
    >
      {multi ? (
        <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
          只修改勾选字段
        </Typography>
      ) : null}
      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { mobile: "1fr", tablet: "1fr 1fr" },
          gap: 2,
        }}
      >
        {trackFields.map((field) => (
          <Box
            key={field}
            sx={field === "lyrics" ? { gridColumn: "1/-1" } : undefined}
          >
            {multi ? (
              <FormControlLabel
                label={fields[field]}
                control={
                  <Checkbox
                    disabled={busy}
                    size="small"
                    color="primary"
                    checked={applied.has(field)}
                    onChange={(e) =>
                      setApplied((old) => {
                        const next = new Set(old);
                        e.target.checked ? next.add(field) : next.delete(field);
                        return next;
                      })
                    }
                  />
                }
              />
            ) : null}
            <TextField
              disabled={busy}
              label={multi ? undefined : fields[field]}
              multiline={field === "lyrics"}
              minRows={field === "lyrics" ? 3 : undefined}
              value={values[field]}
              onChange={(e) => {
                setValues((old) => ({ ...old, [field]: e.target.value }));
                if (multi) setApplied((old) => new Set([...old, field]));
              }}
            />
          </Box>
        ))}
      </Box>
    </Modal>
  );
}
