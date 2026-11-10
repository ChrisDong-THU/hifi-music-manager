import { useMemo, useState } from "react";
import {
  Alert,
  Box,
  Button,
  ButtonBase,
  Card,
  CardContent,
  Collapse,
  Divider,
  TextField,
  Typography,
} from "@mui/material";
import {
  CornerUpLeft,
  AudioLines,
  ChevronDown,
  FileMusic,
  Folder,
  Save,
} from "lucide-react";
import { useAppActions, useAppState } from "./api";
import { libraryMediaURL } from "./http-client";
import { navigate } from "./routing";
import { useConditionalQuery } from "./useConditionalQuery";
import {
  albumIdentity,
  applyAlbumIdentity,
  DraftAlbumMatches,
  editionYear,
  separateAlbumMatch,
  type AlbumMatch,
} from "./DraftAlbumMatches";
import { useProgressNotice } from "./ProgressNotice";
import {
  draftTitle,
  audioQuality,
  bytes,
  duration,
  fields,
  filename,
  type Draft,
  type Tags,
} from "./types";
import { Empty, IconAction, PageTitle, LoadError } from "./components";
import { SectionHeader } from "./upstream/primitives";
import { ImageUpload } from "./ImageUpload";
import { useNotifications } from "./Notifications";
import { hasActiveFileJobs } from "./jobs";

const commonFields = ["album", "album_artist", "year", "genre"] as const;
const trackFields = ["title", "artist", "track", "disc"] as const;
const tagFields = Object.keys(fields) as (keyof Tags)[];
function sameTags(left: Tags, right: Tags) {
  return left === right || tagFields.every((field) => left[field] === right[field]);
}

function initialTags(batch: Draft): Tags[] {
  return batch.files.map(({ tags: tag, editable, path }) => {
    if (batch.has_saved_tags || !editable) return tag;
    const name = filename(path).replace(/\.[^.]+$/, "");
    const named = name.match(/^(.+?) - (.+?)\[(.+)\]$/);
    const numbered = name.match(/^([0-9]{1,3})[ ._-]+(.+)$/);
    return {
      ...tag,
      title: tag.title || named?.[1] || numbered?.[2] || name,
      artist: tag.artist || named?.[2] || "",
      album: tag.album || named?.[3] || "",
      album_artist: tag.album_artist || tag.artist || named?.[2] || "",
      track: tag.track || (numbered ? String(Number(numbered[1])) : ""),
    };
  });
}

// An editor owns one initial snapshot. Background task polling must not replace
// unsaved form values; saving carries the server's content precondition.
export function DraftPage({ id }: { id: string }) {
  const resource = useConditionalQuery<Draft>(
    `drafts/${encodeURIComponent(id)}`,
  );
  useProgressNotice(resource.loading, "读取草稿");
  if (resource.data) return <PreviewPage key={id} batch={resource.data} />;
  return (
    <>
      <PageTitle />
      <LoadError error={resource.error} retry={() => void resource.refresh()} />
    </>
  );
}

function PreviewPage({ batch: initial }: { batch: Draft }) {
  const [batch] = useState(initial);
  const { state } = useAppState();
  const { request, refresh } = useAppActions();
  const notices = useNotifications();
  const [{ tags, albumMatch }, setEdit] = useState(() => ({
    tags: initialTags(batch),
    albumMatch: batch.album_match ?? "",
  }));
  const [saving, setSaving] = useState(false);
  const [activeTrack, setActiveTrack] = useState<string | null>(null);
  const [moreTags, setMoreTags] = useState(false);
  const [error, setError] = useState("");
  const [cover, setCover] = useState<File | null>(null);
  const [failedCover, setFailedCover] = useState("");
  const coverSource = libraryMediaURL(
    `/api/drafts/${batch.id}/cover?v=${batch.cover_hash || batch.files[0]?.hash || "original"}`,
    state?.library_session || "",
  );
  const identity = useMemo(() => albumIdentity(tags), [tags]);
  const changedTracks = useMemo(
    () => tags.map((tag, index) => !sameTags(tag, batch.files[index].tags)),
    [batch, tags],
  );
  const commonTags = useMemo(
    () =>
      commonFields.map((field) => {
        const value = tags[0]?.[field] ?? "";
        return tags.every((tag) => tag[field] === value) ? value : undefined;
      }),
    [tags],
  );
  const dirty =
    !!cover ||
    albumMatch !== (batch.album_match ?? "") ||
    changedTracks.some(Boolean);
  const busy = saving || hasActiveFileJobs(state?.jobs || []);
  const canSave =
    dirty && !busy && (albumMatch !== separateAlbumMatch || identity?.completeYear);
  const editable = !saving;
  const canEditCommon = editable && batch.files.some((file) => file.editable);
  const canEditCover =
    editable &&
    batch.files.length > 0 &&
    batch.files.every((file) => file.editable);
  function updateTags(field: keyof Tags, value: string, onlyIndex?: number) {
    setEdit((previous) => {
      const next = previous.tags.map((tag, index) =>
        (onlyIndex === undefined || onlyIndex === index) &&
        batch.files[index].editable &&
        tag[field] !== value
          ? { ...tag, [field]: value }
          : tag,
      );
      let selected = previous.albumMatch;
      if (
        selected &&
        (field === "album" ||
          field === "album_artist" ||
          field === "artist" ||
          field === "year")
      ) {
        const current = albumIdentity(previous.tags);
        const updated = albumIdentity(next);
        if (
          current?.title !== updated?.title ||
          current?.artist !== updated?.artist ||
          (selected !== separateAlbumMatch &&
            (current?.year !== updated?.year ||
              current?.mixedYears !== updated?.mixedYears))
        )
          selected = "";
      }
      return { tags: next, albumMatch: selected };
    });
  }
  function canApply(album: AlbumMatch) {
    return tags.every((tag, index) => {
      if (batch.files[index].editable) return true;
      return (
        tag.album === album.title &&
        (tag.album_artist || tag.artist) === album.artist &&
        (!album.year || !tag.year || editionYear(tag.year) === album.year)
      );
    });
  }
  function selectAlbum(album: AlbumMatch) {
    setEdit((previous) => ({
      tags: previous.tags.map((tag, index) =>
        batch.files[index].editable ? applyAlbumIdentity(tag, album) : tag,
      ),
      albumMatch: album.key,
    }));
  }
  async function save() {
    if (!canSave) return;
    setSaving(true);
    setError("");
    const noticeID = notices.begin("保存标签", draftTitle(batch));
    try {
      let body: FormData | { tags: Tags[]; album_match: string } = {
        tags,
        album_match: albumMatch,
      };
      if (cover) {
        body = new FormData();
        body.set("tags", JSON.stringify(tags));
        body.set("cover", cover);
        body.set("album_match", albumMatch);
      }
      await request<{ tags: Tags[] }>(
        `drafts/${batch.id}/tags`,
        body,
        { ifMatch: batch.edit_token },
      );
      notices.update(noticeID, { label: "标签已保存", status: "success" });
      navigate("inbox");
      void refresh();
    } catch (e) {
      const message = e instanceof Error ? e.message : "保存失败";
      setError(message);
      notices.update(noticeID, { label: message, status: "error" });
    } finally {
      setSaving(false);
    }
  }
  function editor(field: keyof Tags, index: number) {
    return (
      <TextField
        key={field}
        label={fields[field]}
        value={tags[index]?.[field] || ""}
        onChange={(event) => updateTags(field, event.target.value, index)}
        disabled={!editable || !batch.files[index].editable}
        multiline={field === "lyrics"}
        minRows={field === "lyrics" ? 3 : undefined}
        sx={field === "lyrics" ? { gridColumn: "1 / -1" } : undefined}
      />
    );
  }
  return (
    <>
      <PageTitle
        left={
          <IconAction label="返回草稿" onClick={() => navigate("inbox")}>
            <CornerUpLeft size={20} />
          </IconAction>
        }
      />
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {error ? <Alert severity="error">{error}</Alert> : null}
        <Card data-relay-beat="1" sx={{ p: { mobile: 1, tablet: 2 } }}>
          <SectionHeader
            icon={<Folder size={24} />}
            title={draftTitle(batch)}
            color="secondary.main"
          />
          <CardContent
            sx={{
              px: { mobile: 0.5, tablet: 1 },
              pt: 1.5,
              mt: 1,
              pb: "0!important",
            }}
          >
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 1,
                mb: 2,
              }}
            >
              <Typography variant="caption" color="text.secondary">
                {batch.files.length} 个文件 ·{" "}
                {bytes(batch.files.reduce((sum, file) => sum + file.size, 0))}
              </Typography>
              <Typography
                variant="caption"
                color={dirty ? "warning.light" : "text.secondary"}
              >
                {dirty ? "未保存" : "待导入"}
              </Typography>
            </Box>
            <Box
              className="draft-album-editor"
              sx={{
                display: "grid",
                gridTemplateColumns: {
                  mobile: "minmax(0,1fr)",
                  tablet: "96px minmax(0,1fr)",
                },
                alignItems: "start",
                gap: { mobile: 2, tablet: 3 },
              }}
            >
              <Box sx={{ justifySelf: { mobile: "center", tablet: "start" } }}>
                <ImageUpload
                  file={cover}
                  onChange={setCover}
                  onError={setError}
                  label="选择专辑封面"
                  disabled={!canEditCover}
                  size={96}
                >
                  <Box
                    component="img"
                    src={
                      failedCover === coverSource
                        ? "/default-cover.svg"
                        : coverSource
                    }
                    alt="专辑封面"
                    onError={() => setFailedCover(coverSource)}
                    sx={{ width: "100%", height: "100%", objectFit: "cover" }}
                  />
                </ImageUpload>
              </Box>
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
                {commonFields.map((field, index) => (
                  <TextField
                    key={field}
                    label={fields[field]}
                    value={commonTags[index] ?? ""}
                    placeholder={
                      commonTags[index] === undefined ? "多个值" : undefined
                    }
                    onChange={(event) => updateTags(field, event.target.value)}
                    disabled={!canEditCommon}
                  />
                ))}
              </Box>
            </Box>
            <DraftAlbumMatches
              identity={identity}
              revision={state?.cover_revision ?? 0}
              selection={albumMatch}
              disabled={!editable}
              canApply={canApply}
              onSelect={selectAlbum}
              onSelectionChange={(albumMatch) =>
                setEdit((current) => ({ ...current, albumMatch }))
              }
            />
            {batch.files.some((file) => !file.editable) ? (
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ display: "block", mt: 1.5 }}
              >
                部分格式仅支持保留原标签。
              </Typography>
            ) : null}
          </CardContent>
        </Card>
        <Card data-relay-beat="2" sx={{ p: { mobile: 1, tablet: 2 } }}>
          <SectionHeader
            icon={<AudioLines size={24} />}
            title="曲目"
            color="secondary.main"
          />
          <CardContent
            sx={{
              px: { mobile: 0.5, tablet: 1 },
              pt: 1.5,
              mt: 1,
              pb: "0!important",
            }}
          >
            {!batch.files.length ? (
              <Empty kind="tracks" accent="secondary">
                暂无曲目
              </Empty>
            ) : null}
            {batch.files.map((file, index) => {
              const open = activeTrack === file.id;
              const changed = changedTracks[index];
              return (
                <Box
                  key={file.id}
                  sx={{ borderBottom: "1px solid", borderColor: "divider" }}
                >
                  <ButtonBase
                    className="draft-track-toggle"
                    title={filename(file.path)}
                    aria-label={"编辑 " + filename(file.path)}
                    aria-expanded={open}
                    aria-controls={"draft-track-" + file.id}
                    onClick={() => {
                      setActiveTrack(open ? null : file.id);
                      setMoreTags(false);
                    }}
                  >
                    <FileMusic size={20} />
                    <Box sx={{ minWidth: 0, flex: 1 }}>
                      <Typography variant="body2" noWrap>
                        {tags[index]?.title || filename(file.path)}
                      </Typography>
                      <Typography
                        variant="caption"
                        color="text.secondary"
                        noWrap
                      >
                        {tags[index]?.artist || "未知歌手"} ·{" "}
                        {audioQuality(file)}
                      </Typography>
                    </Box>
                    {changed ? (
                      <Typography
                        variant="caption"
                        color="secondary.light"
                        sx={{ display: { mobile: "none", tablet: "block" } }}
                      >
                        已修改
                      </Typography>
                    ) : null}
                    <Typography variant="caption" color="text.secondary">
                      {duration(file.duration)}
                    </Typography>
                    <ChevronDown
                      size={18}
                      style={{ transform: open ? "rotate(180deg)" : "none" }}
                    />
                  </ButtonBase>
                  <Collapse in={open} unmountOnExit>
                    <Box
                      id={"draft-track-" + file.id}
                      sx={{ p: { mobile: 1, tablet: 2 }, pt: 1 }}
                    >
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
                        {trackFields.map((field) => editor(field, index))}
                      </Box>
                      <Button
                        size="small"
                        color="secondary"
                        endIcon={
                          <ChevronDown
                            size={14}
                            style={{
                              transform: moreTags ? "rotate(180deg)" : "none",
                            }}
                          />
                        }
                        onClick={() => setMoreTags((value) => !value)}
                        sx={{ mt: 1 }}
                      >
                        更多标签
                      </Button>
                      <Collapse in={moreTags}>
                        <Box
                          sx={{
                            display: "grid",
                            gridTemplateColumns: {
                              mobile: "minmax(0,1fr)",
                              tablet: "repeat(2,minmax(0,1fr))",
                            },
                            gap: 2,
                            pt: 1,
                          }}
                        >
                          {[...commonFields, "lyrics" as const].map((field) =>
                            editor(field, index),
                          )}
                        </Box>
                      </Collapse>
                    </Box>
                  </Collapse>
                </Box>
              );
            })}
            <Divider sx={{ mt: 2, mb: 2 }} />
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                flexWrap: "wrap",
                gap: 2,
              }}
            >
              {busy && !saving && dirty ? (
                <Typography variant="caption" color="text.secondary">
                  文件任务结束后可保存修改。
                </Typography>
              ) : null}
              <Box sx={{ display: "flex", gap: 1, ml: "auto" }}>
                <Button
                  color="secondary"
                  variant="contained"
                  startIcon={<Save size={18} />}
                  disabled={!canSave}
                  onClick={() => void save()}
                >
                  保存修改
                </Button>
              </Box>
            </Box>
          </CardContent>
        </Card>
      </Box>
    </>
  );
}
