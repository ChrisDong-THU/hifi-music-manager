import { useState } from "react";
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
function storedTags(batch: Draft): Tags[] {
  return batch.files.map((file) => ({ ...file.tags }));
}
function initialTags(batch: Draft): Tags[] {
  const tags = storedTags(batch);
  if (batch.has_saved_tags) return tags;
  return tags.map((tag, index) => {
    if (!batch.files[index].editable) return tag;
    const name = filename(batch.files[index].path).replace(/\.[^.]+$/, "");
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
  if (resource.data) return <PreviewPage batch={resource.data} />;
  return (
    <>
      <PageTitle />
      <LoadError error={resource.error} retry={() => void resource.refresh()} />
    </>
  );
}

function PreviewPage({ batch }: { batch: Draft }) {
  const { state } = useAppState();
  const { request, refresh } = useAppActions();
  const notices = useNotifications();
  const [tags, setTags] = useState(() => initialTags(batch));
  const [saved, setSaved] = useState(() => storedTags(batch));
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
  const dirty = !!cover || JSON.stringify(tags) !== JSON.stringify(saved);
  const busy = saving || hasActiveFileJobs(state?.jobs || []);
  const editable = !busy;
  const canEditCommon = editable && batch.files.some((file) => file.editable);
  const canEditCover =
    editable &&
    batch.files.length > 0 &&
    batch.files.every((file) => file.editable);
  function updateTrack(index: number, field: keyof Tags, value: string) {
    setTags((previous) =>
      previous.map((tag, i) =>
        i === index ? { ...tag, [field]: value } : tag,
      ),
    );
  }
  function commonValue(field: keyof Tags) {
    return tags.every((tag) => tag[field] === tags[0]?.[field])
      ? tags[0]?.[field] || ""
      : "";
  }
  async function save() {
    if (!dirty || busy) return;
    setSaving(true);
    setError("");
    const noticeID = notices.begin("保存标签", draftTitle(batch));
    try {
      let body: FormData | { tags: Tags[] } = { tags };
      if (cover) {
        body = new FormData();
        body.set("tags", JSON.stringify(tags));
        body.set("cover", cover);
      }
      const result = await request<{ tags: Tags[] }>(
        `drafts/${batch.id}/tags`,
        body,
        { ifMatch: batch.edit_token },
      );
      setTags(result.tags.map((tag) => ({ ...tag })));
      setSaved(result.tags.map((tag) => ({ ...tag })));
      await refresh();
      setCover(null);
      notices.update(noticeID, { label: "标签已保存", status: "success" });
      navigate("inbox");
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
      notices.update(noticeID, {
        label: e instanceof Error ? e.message : "保存失败",
        status: "error",
      });
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
        onChange={(event) => updateTrack(index, field, event.target.value)}
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
                {commonFields.map((field) => (
                  <TextField
                    key={field}
                    label={fields[field]}
                    value={commonValue(field)}
                    placeholder={
                      tags.every((tag) => tag[field] === tags[0]?.[field])
                        ? undefined
                        : "多个值"
                    }
                    onChange={(event) =>
                      setTags((previous) =>
                        previous.map((tag, index) =>
                          batch.files[index].editable
                            ? { ...tag, [field]: event.target.value }
                            : tag,
                        ),
                      )
                    }
                    disabled={!canEditCommon}
                  />
                ))}
              </Box>
            </Box>
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
              const changed = Object.keys(fields).some(
                (field) =>
                  tags[index]?.[field as keyof Tags] !==
                  file.tags[field as keyof Tags],
              );
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
              <Box sx={{ display: "flex", gap: 1, ml: "auto" }}>
                <Button
                  color="secondary"
                  variant="contained"
                  startIcon={<Save size={18} />}
                  disabled={!dirty || busy}
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
