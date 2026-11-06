// Inbox tree, accent header and action placement follow beets-flask's InboxCard.
import { useEffect, useState } from "react";
import {
  Box,
  Button,
  Card,
  CardActions,
  Checkbox,
  Collapse,
  Divider,
  MenuItem,
  TextField,
  Typography,
} from "@mui/material";
import {
  ChevronDown,
  ChevronRight,
  Disc3,
  FileMusic,
  Import,
  Inbox as InboxIcon,
  ListChecks,
  ListX,
  Play,
  RefreshCw,
  Settings,
  Trash2,
} from "lucide-react";
import { useAppActions, useAppState, useImportPreferences } from "./api";
import { navigate } from "./routing";
import { useConditionalQuery } from "./useConditionalQuery";
import { useProgressNotice } from "./ProgressNotice";
import { audioQuality, draftTitle, bytes, filename } from "./types";
import {
  Confirm,
  Empty,
  IconAction,
  Modal,
  PageTitle,
  LoadError,
} from "./components";
import { AccentHeader } from "./upstream/primitives";
import { UploadArea, type useUpload } from "./upload";
import { hasUploadFiles, isTextEditing } from "./upload-files";
import type { Playback, Draft } from "./types";
import { ScrollableCardContent } from "./ScrollableCardContent";
import { hasActiveFileJobs } from "./jobs";
import { AudioQuality } from "./AudioQuality";
import { DraftStages } from "./DraftStages";
import { DraftOverallStatus } from "./DraftOverallStatus";
import { DeleteMusicButton } from "./DeleteMusicButton";
import {
  draftFileKey,
  draftSelection,
  pruneDraftSelection,
  toggleDraftFiles,
} from "./draft-selection";

const noDrafts: Draft[] = [];

export function InboxPage({
  onPlay,
  upload,
}: {
  onPlay: (value: Playback) => void;
  upload: ReturnType<typeof useUpload>;
}) {
  const { state } = useAppState();
  const { refresh, enqueue } = useAppActions();
  const { preferences, setPreferences } = useImportPreferences();
  const drafts = useConditionalQuery<{ drafts: Draft[]; revision: number }>(
    "drafts",
    state?.revision ?? 0,
    state?.storage?.ready !== false,
  );
  const batches = drafts.data?.drafts || noDrafts;
  useProgressNotice(drafts.loading, "加载草稿");
  async function refreshDrafts() {
    await refresh();
    await drafts.refresh();
  }
  const [selected, setSelected] = useState(new Set<string>()),
    [expanded, setExpanded] = useState(new Set<string>()),
    [settings, setSettings] = useState(false),
    [submitting, setSubmitting] = useState(false),
    [drag, setDrag] = useState(false);
  useEffect(() => {
    setSelected((old) => pruneDraftSelection(batches, old));
  }, [batches]);
  useEffect(() => {
    const paste = (event: ClipboardEvent) => {
      if (
        event.defaultPrevented ||
        isTextEditing(event.target) ||
        !event.clipboardData ||
        !hasUploadFiles(event.clipboardData)
      )
        return;
      event.preventDefault();
      void upload.paste(event.clipboardData);
    };
    document.addEventListener("paste", paste);
    return () => document.removeEventListener("paste", paste);
  }, [upload.paste]);
  if (!state) return null;
  const chosen = draftSelection(batches, selected);
  const selectedCount = chosen.reduce(
    (sum, entry) => sum + entry.files.length,
    0,
  );
  const totalCount = batches.reduce(
    (sum, batch) => sum + batch.files.length,
    0,
  );
  const allSelected = totalCount > 0 && selectedCount === totalCount;
  const busy = submitting || upload.isBusy || hasActiveFileJobs(state.jobs);
  const canDelete = chosen.length > 0 && !busy;
  const inboxPath = state.uploads_path.replaceAll("\\", "/");
  const pathSegments = inboxPath.split("/").filter(Boolean);
  const toggle = (batchID: string, fileIDs: string[]) =>
    setSelected((old) => toggleDraftFiles(old, batchID, fileIDs));
  const expand = (id: string) =>
    setExpanded((old) => {
      const next = new Set(old);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  async function processSelected(kind: "import" | "delete") {
    setSubmitting(true);
    try {
      for (const { batch, files } of chosen) {
        await enqueue(
          `drafts/${batch.id}/${kind}`,
          {
            ids: files.map((file) => file.id),
            ...(kind === "import"
              ? {
                  duplicate_policy: preferences.duplicate_policy,
                }
              : {}),
          },
          { ifMatch: batch.edit_token },
        );
        setSelected((old) => {
          const next = new Set(old);
          for (const file of files)
            next.delete(draftFileKey(batch.id, file.id));
          return next;
        });
      }
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <>
      <PageTitle
        left={
          <IconAction label="刷新草稿" onClick={() => void refreshDrafts()}>
            <RefreshCw size={20} />
          </IconAction>
        }
        right={
          <IconAction label="导入设置" onClick={() => setSettings(true)}>
            <Settings size={20} />
          </IconAction>
        }
      />
      <LoadError error={drafts.error} retry={() => void refreshDrafts()} />
      <Box className="inbox-workspace">
        <Card
          className="inbox-drafts-card scroll-panel"
          component="section"
          aria-label="上传条目"
          sx={{
            width: "100%",
            p: { mobile: 1, tablet: 2 },
            outline: drag ? "2px dashed var(--page-accent)" : "none",
            outlineOffset: 4,
          }}
          onDragOver={(e) => {
            if (!hasUploadFiles(e.dataTransfer)) return;
            e.preventDefault();
            setDrag(true);
          }}
          onDragLeave={(e) => {
            if (!e.currentTarget.contains(e.relatedTarget as Node))
              setDrag(false);
          }}
          onDrop={(e) => {
            setDrag(false);
            void upload.drop(e);
          }}
        >
          <AccentHeader
            className="panel-header"
            data-relay-beat="1"
            icon={<InboxIcon size={24} />}
            color="secondary.main"
            dividerPos="70%"
          >
            <Box
              className="inbox-header-content"
              sx={{
                flex: 1,
                minWidth: 0,
                display: "flex",
                justifyContent: "space-between",
                position: "relative",
                pl: 1,
                pr: 1,
                pb: 2.5,
                alignItems: "flex-end",
              }}
            >
              <Box
                title={state.uploads_path}
                sx={{
                  display: "flex",
                  flexDirection: "row",
                  flexWrap: { mobile: "nowrap", laptop: "wrap" },
                  alignItems: "flex-end",
                  width: "100%",
                  minWidth: 0,
                  columnGap: 0.5,
                  rowGap: 0.1,
                  overflow: "hidden",
                }}
              >
                {pathSegments.map((segment, index) => (
                  <Typography
                    key={index}
                    variant="body2"
                    component="span"
                    sx={{ display: "inline-flex", whiteSpace: "nowrap" }}
                  >
                    {index === 0 && /^[a-z]:$/i.test(segment)
                      ? segment
                      : `/ ${segment}`}
                  </Typography>
                ))}
              </Box>
            </Box>
          </AccentHeader>
          <Box
            className="panel-header"
            data-relay-beat="1"
            sx={{ px: { mobile: 0.5, tablet: 1 }, pt: 1.5, mt: 1 }}
          >
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: "24px minmax(0, 1fr)",
                alignItems: "center",
                columnGap: { mobile: "5px", tablet: "12px" },
                px: { mobile: "2px", tablet: "4px" },
                pb: 1,
              }}
            >
              <Box
                sx={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  "& > span": { display: "inline-flex" },
                }}
              >
                <IconAction
                  label={allSelected ? "取消全选" : "选择全部"}
                  disabled={!totalCount || submitting}
                  onClick={() =>
                    setSelected(
                      allSelected
                        ? new Set()
                        : new Set(
                            batches.flatMap((b) =>
                              b.files.map((f) => draftFileKey(b.id, f.id)),
                            ),
                          ),
                    )
                  }
                >
                  {allSelected ? <ListX size={20} /> : <ListChecks size={20} />}
                </IconAction>
              </Box>
              <Typography variant="caption" color="text.secondary">
                {selectedCount
                  ? `已选 ${selectedCount} 首`
                  : `${totalCount} 首歌曲`}
              </Typography>
            </Box>
          </Box>
          <ScrollableCardContent
            relayBeat={2}
            className="inbox-drafts-list"
            label="上传条目列表"
          >
            <Box className="inbox-tree">
              {batches.length ? (
                batches.map((b) => {
                  const title = draftTitle(b);
                  const fileIDs = b.files.map((file) => file.id);
                  const selectedFiles = b.files.filter((file) =>
                    selected.has(draftFileKey(b.id, file.id)),
                  ).length;
                  const checked =
                    fileIDs.length > 0 && selectedFiles === fileIDs.length;
                  const partial = selectedFiles > 0 && !checked;
                  const duplicates = new Set(b.duplicates);
                  return (
                    <Box key={b.id}>
                      <Box
                        className="folder-row"
                        data-selected={checked || partial}
                        onClick={() => !submitting && toggle(b.id, fileIDs)}
                      >
                        <Checkbox
                          color="secondary"
                          size="medium"
                          sx={{ p: 0 }}
                          checked={checked}
                          indeterminate={partial}
                          disabled={!fileIDs.length || submitting}
                          onClick={(e) => e.stopPropagation()}
                          onChange={() => toggle(b.id, fileIDs)}
                          inputProps={{
                            "aria-label": "选择目录 " + title,
                            "aria-checked": partial ? "mixed" : checked,
                          }}
                        />
                        <Box
                          className="folder-name"
                          sx={{
                            display: "flex",
                            alignItems: "center",
                            gap: 0.7,
                            minWidth: 0,
                          }}
                        >
                          <IconAction
                            tooltip=""
                            label={
                              (expanded.has(b.id) ? "折叠 " : "展开 ") + title
                            }
                            onClick={() => expand(b.id)}
                          >
                            {expanded.has(b.id) ? (
                              <ChevronDown size={20} />
                            ) : (
                              <ChevronRight size={20} />
                            )}
                          </IconAction>
                          <Disc3
                            size={20}
                            style={{
                              flexShrink: 0,
                              transform: expanded.has(b.id)
                                ? "rotate(90deg)"
                                : "none",
                              transition: "transform .15s",
                            }}
                          />
                          <Button
                            color="inherit"
                            onClick={(e) => {
                              e.stopPropagation();
                              navigate("inbox?draft=" + b.id);
                            }}
                            sx={{
                              textTransform: "none",
                              fontWeight: 400,
                              minWidth: 0,
                              p: 0,
                              justifyContent: "flex-start",
                              textAlign: "left",
                            }}
                          >
                            <Typography noWrap title={title}>
                              {title}
                            </Typography>
                          </Button>
                        </Box>
                        <Box className="folder-chips">
                          <DraftOverallStatus batch={b} />
                          <DraftStages batch={b} disabled={upload.isBusy} />
                        </Box>
                        {!b.files.length ? (
                          <Confirm
                            title="移除空草稿"
                            text="此草稿没有已保存歌曲，确认移除这个空条目？"
                            color="secondary"
                            onConfirm={async () => {
                              await enqueue(`drafts/${b.id}/discard`);
                            }}
                          >
                            {(open) => (
                              <IconAction
                                label={`移除空草稿 ${title}`}
                                tooltip="移除空草稿"
                                disabled={busy}
                                onClick={open}
                              >
                                <Trash2 size={16} />
                              </IconAction>
                            )}
                          </Confirm>
                        ) : (
                          <IconAction
                            tooltip="播放"
                            label={"播放 " + title}
                            disabled={!b.files.length}
                            onClick={() => {
                              const queue = b.files;
                              if (queue[0])
                                onPlay({
                                  track: queue[0],
                                  queue,
                                  batchID: b.id,
                                });
                            }}
                          >
                            <Play size={16} />
                          </IconAction>
                        )}
                      </Box>
                      <Collapse in={expanded.has(b.id)}>
                        <Box className="file-rows">
                          {!b.files.length ? (
                            <Empty kind="tracks" accent="secondary">
                              暂无文件
                            </Empty>
                          ) : null}
                          {b.files.map((file) => (
                            <Box
                              key={file.id}
                              className="file-row"
                              data-selected={selected.has(
                                draftFileKey(b.id, file.id),
                              )}
                              onClick={(event) => {
                                if (
                                  !submitting &&
                                  event.currentTarget.contains(
                                    event.target as Node,
                                  )
                                )
                                  toggle(b.id, [file.id]);
                              }}
                            >
                              <Checkbox
                                color="secondary"
                                size="small"
                                sx={{ p: 0 }}
                                checked={selected.has(
                                  draftFileKey(b.id, file.id),
                                )}
                                disabled={submitting}
                                onClick={(event) => event.stopPropagation()}
                                onChange={() => toggle(b.id, [file.id])}
                                inputProps={{
                                  "aria-label":
                                    "选择歌曲 " + filename(file.path),
                                }}
                              />
                              <FileMusic size={16} />
                              <Typography
                                variant="body2"
                                noWrap
                                className="file-row-name"
                                title={filename(file.path)}
                              >
                                {filename(file.path)}
                              </Typography>
                              <Box className="file-row-indicators">
                                {duplicates.has(file.id) ? (
                                  <Box
                                    component="span"
                                    className="file-row-quality duplicate-label"
                                    aria-label="重复歌曲"
                                    title="与曲库或目录内其他歌曲的文件或音频内容重复"
                                  >
                                    重复
                                  </Box>
                                ) : null}
                                <AudioQuality
                                  track={file}
                                  result={b.quality?.[file.id]}
                                  running={b.quality_state === "running"}
                                />
                                <Box
                                  component="span"
                                  className="file-row-quality"
                                  aria-label={`标称音质：${audioQuality(file)}`}
                                  title={`${audioQuality(file)} · ${bytes(file.size)}`}
                                >
                                  {audioQuality(file)}
                                </Box>
                              </Box>
                              <Typography
                                variant="caption"
                                className="file-row-size"
                              >
                                {bytes(file.size)}
                              </Typography>
                            </Box>
                          ))}
                        </Box>
                      </Collapse>
                    </Box>
                  );
                })
              ) : (
                <Empty kind="inbox" accent="secondary" loading={drafts.loading}>
                  拖入音乐文件或文件夹
                </Empty>
              )}
            </Box>
          </ScrollableCardContent>
          <Divider sx={{ mx: 1 }} />
          <CardActions
            className="inbox-drafts-actions"
            data-relay-beat="3"
            sx={{
              display: "flex",
              pt: 2,
              px: 1,
            }}
          >
            <Box
              sx={{
                display: "flex",
                justifyContent: "space-between",
                gap: 1,
                width: "100%",
                flexWrap: "wrap",
                alignItems: "center",
              }}
            >
              <Box
                role="group"
                aria-label="草稿操作"
                sx={{
                  display: "flex",
                  gap: 0.5,
                }}
              >
                <DeleteMusicButton
                  title="删除所选草稿"
                  description={`将所选 ${selectedCount} 首歌曲的上传文件永久删除。`}
                  confirmColor="error"
                  startIcon={<Trash2 size={18} />}
                  disabled={!canDelete}
                  onDelete={() => processSelected("delete")}
                />
              </Box>
              <Confirm
                title="导入所选歌曲"
                text={`按已保存的标签导入 ${selectedCount} 首歌曲。`}
                onConfirm={() => processSelected("import")}
              >
                {(open) => (
                  <Button
                    color="secondary"
                    variant="contained"
                    startIcon={<Import size={18} />}
                    disabled={busy || !chosen.length}
                    onClick={open}
                  >
                    导入
                  </Button>
                )}
              </Confirm>
            </Box>
          </CardActions>
        </Card>
        <UploadArea upload={upload} />
      </Box>
      <Modal
        title="导入设置"
        open={settings}
        onClose={() => setSettings(false)}
        actions={
          <Button color="secondary" onClick={() => setSettings(false)}>
            完成
          </Button>
        }
      >
        <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
          <TextField
            select
            label="重复文件"
            value={preferences.duplicate_policy}
            onChange={(event) =>
              setPreferences({
                duplicate_policy: event.target
                  .value as typeof preferences.duplicate_policy,
              })
            }
          >
            <MenuItem value="skip">跳过重复文件</MenuItem>
            <MenuItem value="keep">保留副本</MenuItem>
            <MenuItem value="reject">发现重复时停止</MenuItem>
          </TextField>
        </Box>
      </Modal>
    </>
  );
}
