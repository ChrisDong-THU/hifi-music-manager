import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type DragEvent,
} from "react";
import { Box, Button, ButtonBase, Typography } from "@mui/material";
import { FileMusic, FolderUp, RotateCw } from "lucide-react";
import { useAppActions, useLibraryState } from "./api";
import { ProgressNotice, useProgressNotice } from "./ProgressNotice";
import { uploadFile } from "./upload-request";
import {
  collectUploadFiles,
  hasUploadFiles,
  isAudioFile,
  type UploadItem,
} from "./upload-files";

type UploadFile = { file: File; path: string; done: boolean; error: string };

export function useUpload() {
  const { csrf, session } = useLibraryState();
  const { request, enqueue, refresh, notify } = useAppActions();
  const filesInput = useRef<HTMLInputElement>(null),
    folderInput = useRef<HTMLInputElement>(null);
  const pending = useRef<UploadFile[]>([]),
    batch = useRef("");
  const running = useRef(false);
  const intake = useRef(false);
  const lifetime = useRef(new AbortController());
  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => controller.abort();
  }, []);
  const [accepting, setAccepting] = useState(false);
  const [collecting, setCollecting] = useState(false);
  useProgressNotice(collecting, "读取上传文件");
  const [progress, setProgress] = useState<{
    name: string;
    percent: number;
    done: number;
    total: number;
    running: boolean;
    errors: string[];
  } | null>(null);
  const transmit = useCallback(
    (item: UploadFile, update: (n: number) => void) =>
      uploadFile(
        `drafts/${batch.current}/files?path=${encodeURIComponent(item.path)}`,
        item.file,
        csrf,
        update,
        lifetime.current.signal,
        session,
      ),
    [csrf, session],
  );
  const resume = useCallback(async () => {
    const signal = lifetime.current.signal;
    if (running.current || signal.aborted) return;
    running.current = true;
    try {
      const list = pending.current;
      for (const item of list) {
        if (signal.aborted) return;
        if (item.done) continue;
        item.error = "";
        const update = (fraction: number) =>
          !signal.aborted &&
          setProgress({
            name: item.path,
            percent:
              ((list.filter((x) => x.done).length + fraction) / list.length) *
              100,
            done: list.filter((x) => x.done).length,
            total: list.length,
            running: true,
            errors: list
              .filter((x) => x.error)
              .map((x) => x.path + "：" + x.error),
          });
        update(0);
        try {
          await transmit(item, update);
          item.done = true;
        } catch (e) {
          if (signal.aborted) return;
          item.error = e instanceof Error ? e.message : "上传失败";
        }
      }
      if (signal.aborted) return;
      const done = list.filter((x) => x.done).length;
      setProgress({
        name: "上传完成",
        percent: (done / list.length) * 100,
        done,
        total: list.length,
        running: false,
        errors: list.filter((x) => x.error).map((x) => x.path + "：" + x.error),
      });
      if (done === list.length) {
        try {
          await enqueue(`drafts/${batch.current}/prepare`);
        } catch {
          // Draft labels own preparation status and retry. Upload notices
          // must not create a second processing toast.
        }
        pending.current = [];
      }
      if (!signal.aborted) await refresh();
    } finally {
      running.current = false;
    }
  }, [transmit, enqueue, refresh]);
  const start = useCallback(
    async (source: UploadItem[] | (() => Promise<UploadItem[]>)) => {
      if (intake.current || running.current) {
        notify("请等待当前上传完成", true);
        return;
      }
      intake.current = true;
      const signal = lifetime.current.signal;
      setAccepting(true);
      setCollecting(true);
      setProgress(null);
      try {
        const items = typeof source === "function" ? await source() : source;
        if (signal.aborted) return;
        const audio = items.filter((x) => isAudioFile(x.path));
        if (!audio.length) {
          notify("没有找到支持的音乐文件", true);
          return;
        }
        if (audio.length > 300) {
          notify("每批最多 300 首，请分批上传", true);
          return;
        }
        const name = audio[0].path.includes("/")
          ? audio[0].path.split("/")[0]
          : audio.length === 1
            ? audio[0].file.name.replace(/\.[^.]+$/, "")
            : "上传 " +
              new Date().toLocaleTimeString("zh-CN", { hour12: false });
        const b = await request<{ id: string }>("drafts", { name }, { signal });
        if (signal.aborted) return;
        batch.current = b.id;
        setCollecting(false);
        pending.current = audio.map((item) => ({
          ...item,
          done: false,
          error: "",
        }));
        await resume();
      } catch (e) {
        if (!signal.aborted)
          notify(e instanceof Error ? e.message : "上传失败", true);
      } finally {
        intake.current = false;
        if (!signal.aborted) {
          setAccepting(false);
          setCollecting(false);
        }
      }
    },
    [notify, request, resume],
  );
  const drop = useCallback(
    (e: DragEvent) => {
      if (!hasUploadFiles(e.dataTransfer)) return;
      e.preventDefault();
      e.stopPropagation();
      // Capture before asynchronous work so pasted/dropped browser entries stay readable.
      const transfer = e.dataTransfer;
      return start(() => collectUploadFiles(transfer, lifetime.current.signal));
    },
    [start],
  );
  const paste = useCallback(
    (transfer: DataTransfer) =>
      start(() => collectUploadFiles(transfer, lifetime.current.signal)),
    [start],
  );
  const isBusy = accepting || !!progress?.running;
  const controls = (
    <>
      <input
        ref={filesInput}
        type="file"
        hidden
        multiple
        accept=".flac,.mp3,.m4a,.ogg,.opus,.wav,.aiff,.aif"
        onChange={(e) => {
          void start(
            Array.from(e.target.files || []).map((file) => ({
              file,
              path: file.name,
            })),
          );
          e.target.value = "";
        }}
      />
      <input
        ref={folderInput}
        type="file"
        hidden
        multiple
        {...{ webkitdirectory: "" }}
        onChange={(e) => {
          void start(
            Array.from(e.target.files || []).map((file) => ({
              file,
              path: file.webkitRelativePath || file.name,
            })),
          );
          e.target.value = "";
        }}
      />
    </>
  );
  const progressView = progress ? (
    <ProgressNotice
      label={
        progress.running
          ? "上传音乐"
          : progress.errors.length
            ? "上传未完成"
            : "完成上传音乐"
      }
      detail={progress.running ? progress.name : undefined}
      count={`${progress.done}/${progress.total} · ${Math.round(progress.percent)}%`}
      value={progress.percent}
      status={
        progress.running
          ? "active"
          : progress.errors.length
            ? "error"
            : "success"
      }
      onClose={!progress.running ? () => setProgress(null) : undefined}
      closeLabel="关闭上传进度"
    >
      {!progress.running && progress.errors.length ? (
        <>
          <Typography
            color="error"
            variant="caption"
            sx={{
              display: "block",
              mt: 1,
              whiteSpace: "pre-line",
              overflowWrap: "anywhere",
            }}
          >
            {progress.errors.join("\n")}
          </Typography>
          <Button
            size="small"
            color="secondary"
            startIcon={<RotateCw size={16} />}
            disabled={isBusy}
            onClick={() => void resume()}
          >
            重试失败文件
          </Button>
        </>
      ) : null}
    </ProgressNotice>
  ) : null;
  return {
    controls,
    progressView,
    drop,
    paste,
    isBusy,
    openFiles: () => filesInput.current?.click(),
    openFolder: () => folderInput.current?.click(),
  };
}

export function UploadArea({
  upload,
}: {
  upload: ReturnType<typeof useUpload>;
}) {
  const [dragging, setDragging] = useState(false);
  const [mode, setMode] = useState<"files" | "folder">("files");
  const dragDepth = useRef(0);
  return (
    <Box
      component="section"
      className="inbox-upload-area"
      data-relay-beat="4"
      aria-label="音乐上传"
      aria-busy={upload.isBusy}
      data-dragging={dragging}
      onDragEnter={(event) => {
        if (!hasUploadFiles(event.dataTransfer) || upload.isBusy) return;
        event.preventDefault();
        event.stopPropagation();
        dragDepth.current++;
        setDragging(true);
      }}
      onDragOver={(event) => {
        if (!hasUploadFiles(event.dataTransfer)) return;
        event.preventDefault();
        event.stopPropagation();
        event.dataTransfer.dropEffect = upload.isBusy ? "none" : "copy";
      }}
      onDragLeave={(event) => {
        event.stopPropagation();
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (!dragDepth.current) setDragging(false);
      }}
      onDrop={(event) => {
        if (!hasUploadFiles(event.dataTransfer)) return;
        event.preventDefault();
        event.stopPropagation();
        dragDepth.current = 0;
        setDragging(false);
        if (!upload.isBusy) void upload.drop(event);
      }}
    >
      <Box
        className="upload-mode-switch"
        role="group"
        aria-label="上传类型"
        data-mode={mode}
      >
        <ButtonBase
          className="upload-mode"
          aria-pressed={mode === "files"}
          disabled={upload.isBusy}
          onClick={() => setMode("files")}
        >
          <FileMusic size={16} />
          音乐文件
        </ButtonBase>
        <ButtonBase
          className="upload-mode"
          aria-pressed={mode === "folder"}
          disabled={upload.isBusy}
          onClick={() => setMode("folder")}
        >
          <FolderUp size={16} />
          专辑文件夹
        </ButtonBase>
      </Box>
      <ButtonBase
        className="upload-dropzone"
        aria-label={mode === "files" ? "上传音乐" : "上传文件夹"}
        data-dragging={dragging}
        aria-disabled={upload.isBusy}
        tabIndex={upload.isBusy ? -1 : 0}
        onClick={() => {
          if (!upload.isBusy)
            (mode === "files" ? upload.openFiles : upload.openFolder)();
        }}
      >
        <Box
          component="span"
          className="upload-dropzone-icon"
          aria-hidden="true"
        >
          {mode === "files" ? (
            <FileMusic size={28} strokeWidth={1.5} />
          ) : (
            <FolderUp size={28} strokeWidth={1.5} />
          )}
        </Box>
        <Box component="span" className="upload-dropzone-copy">
          <Box
            component="span"
            className="upload-dropzone-title"
            role="status"
            aria-live="polite"
          >
            {upload.isBusy
              ? "正在上传音乐…"
              : dragging
                ? "松开即可上传"
                : mode === "files"
                  ? "点击选择音乐文件"
                  : "点击选择专辑文件夹"}
          </Box>
          <Box component="span" className="upload-dropzone-label">
            {upload.isBusy
              ? "上传进度将在右上角显示"
              : "也可将音乐文件或文件夹拖入此处"}
          </Box>
        </Box>
      </ButtonBase>
    </Box>
  );
}
