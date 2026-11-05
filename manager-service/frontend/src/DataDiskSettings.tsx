import { useEffect, useState } from "react";
import {
  Box,
  Button,
  Chip,
  MenuItem,
  TextField,
  Typography,
} from "@mui/material";
import { RotateCw, Settings } from "lucide-react";
import { useAppActions, useAppState } from "./api";
import { bytes } from "./types";
import { Empty, IconAction, inlineCodeSx, Modal } from "./components";

type Disk = {
  device: string;
  root: string;
  data_root: string;
  mount_id: string;
  name: string;
  size: number;
  free: number;
  writable: boolean;
  initialized: boolean;
  volume_id?: string;
  issue?: string;
};
type DiskList = {
  enabled: boolean;
  disks: Disk[];
  selected_id?: string;
  active_id?: string;
};
const diskKey = (disk: Disk) =>
  `${disk.device}\0${disk.mount_id}\0${disk.root}`;

export function DataDiskSettings() {
  const { state } = useAppState();
  const { request, enqueue, notify } = useAppActions();
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<DiskList | null>(null);
  const [selected, setSelected] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const busy =
    submitting ||
    Boolean(
      state?.jobs.some(
        (job) =>
          job.kind === "disks" && ["queued", "running"].includes(job.state),
      ),
    );
  const [error, setError] = useState("");
  const [generation, setGeneration] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setData(null);
    setError("");
    setConfirm(false);
    request<DiskList>("system/disks", undefined, { signal: controller.signal })
      .then((result) => {
        if (controller.signal.aborted) return;
        setData(result);
        const current = result.disks.find(
          (disk) => disk.volume_id === (result.selected_id || result.active_id),
        );
        setSelected(current ? diskKey(current) : "");
      })
      .catch((err: Error) => {
        if (!controller.signal.aborted) {
          setError(err.message);
          notify(err.message, true);
        }
      });
    return () => controller.abort();
  }, [open, generation, request, notify]);
  const disk = data?.disks.find((entry) => diskKey(entry) === selected);
  const unchanged = Boolean(
    disk?.initialized && disk.volume_id === data?.selected_id,
  );
  async function save() {
    if (!disk || busy) return;
    if (!disk.initialized && !confirm) {
      setConfirm(true);
      return;
    }
    setSubmitting(true);
    setOpen(false);
    try {
      await enqueue("system/disks", {
        device: disk.device,
        root: disk.root,
        mount_id: disk.mount_id,
        initialize: !disk.initialized,
        confirmed: confirm,
      });
    } catch {
      // enqueue reports submission failures through the shared notice lifecycle.
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <>
      <IconAction
        label="数据盘设置"
        onClick={() => {
          setOpen(true);
          setConfirm(false);
        }}
      >
        <Settings size={20} />
      </IconAction>
      <Modal
        title={confirm ? "初始化数据盘" : "数据盘设置"}
        open={open}
        onClose={() => {
          if (!busy) setOpen(false);
        }}
        actions={
          <>
            <Box
              aria-hidden={confirm || undefined}
              inert={confirm}
              sx={{ visibility: confirm ? "hidden" : "visible" }}
            >
              <IconAction
                label="重新搜索数据盘"
                disabled={busy || confirm}
                onClick={() => setGeneration((value) => value + 1)}
              >
                <RotateCw size={18} />
              </IconAction>
            </Box>
            <Box sx={{ flex: 1 }} />
            <Button
              color="secondary"
              disabled={busy}
              onClick={() => (confirm ? setConfirm(false) : setOpen(false))}
            >
              {confirm ? "返回" : "取消"}
            </Button>
            <Button
              color="secondary"
              variant="contained"
              disabled={
                busy ||
                !disk ||
                !disk.writable ||
                Boolean(disk.issue) ||
                unchanged ||
                !data?.enabled
              }
              onClick={() => void save()}
            >
              {busy
                ? "正在设置"
                : disk && !disk.initialized
                  ? "初始化并使用"
                  : "使用此盘"}
            </Button>
          </>
        }
      >
        <Box sx={{ display: "grid", alignItems: "center" }}>
          <Box
            aria-hidden={confirm || undefined}
            inert={confirm}
            sx={{
              gridArea: "1 / 1",
              visibility: confirm ? "hidden" : "visible",
              minWidth: 0,
            }}
          >
            {data?.enabled && data.disks.length ? (
              <TextField
                select
                label="数据盘"
                value={selected}
                disabled={busy || confirm}
                onChange={(event) => setSelected(event.target.value)}
                helperText={
                  disk
                    ? `${bytes(disk.free)} 可用 · 共 ${bytes(disk.size)}`
                    : " "
                }
              >
                {data.disks.map((entry) => {
                  return (
                    <MenuItem
                      key={diskKey(entry)}
                      value={diskKey(entry)}
                      disabled={!entry.writable || Boolean(entry.issue)}
                    >
                      <Box
                        component="span"
                        sx={{
                          display: "flex",
                          gap: 1,
                          alignItems: "center",
                          flexWrap: "wrap",
                        }}
                      >
                        <span>
                          {entry.name} · {entry.root}
                        </span>
                        {!entry.initialized ? (
                          <Chip
                            size="small"
                            label="未初始化"
                            variant="outlined"
                          />
                        ) : null}
                        {entry.volume_id &&
                        entry.volume_id === data.active_id ? (
                          <Chip
                            size="small"
                            label="当前数据盘"
                            color="primary"
                            variant="outlined"
                          />
                        ) : null}
                        {!entry.writable ? (
                          <Chip size="small" label="只读" variant="outlined" />
                        ) : null}
                        {entry.issue ? (
                          <Typography variant="caption" color="text.secondary">
                            {entry.issue}
                          </Typography>
                        ) : null}
                      </Box>
                    </MenuItem>
                  );
                })}
              </TextField>
            ) : (
              <Empty kind="folder" loading={!data && !error}>
                {data?.enabled
                  ? "未发现已挂载的 U 盘"
                  : data
                    ? "此服务未启用数据盘管理"
                    : error
                      ? "无法读取数据盘"
                      : "正在读取数据盘"}
              </Empty>
            )}
          </Box>
          <Box
            aria-hidden={!confirm || undefined}
            inert={!confirm}
            sx={{
              gridArea: "1 / 1",
              visibility: confirm ? "visible" : "hidden",
              minWidth: 0,
            }}
          >
            <Typography
              component="div"
              variant="body2"
              color="text.secondary"
              sx={{ lineHeight: 2.3 }}
            >
              确定在
              <Box component="code" sx={inlineCodeSx}>
                {disk?.name || "所选数据盘"}
              </Box>
              上创建新目录
              <Box component="code" sx={inlineCodeSx}>
                hifi-music-nano
              </Box>
              吗？
            </Typography>
          </Box>
        </Box>
      </Modal>
    </>
  );
}
