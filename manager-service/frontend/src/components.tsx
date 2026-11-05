import { useId, useState, type ReactNode } from "react";
import "@fontsource/bodoni-moda/latin-600-italic.css";
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  Typography,
  Tooltip,
} from "@mui/material";
import {
  AudioLines,
  Disc3,
  FileMusic,
  FolderOpen,
  History,
  Inbox as InboxIcon,
  SearchX,
  Tag,
  Trash2,
  UserRound,
  X,
} from "lucide-react";
import { alpha, type Theme } from "@mui/material/styles";
import { useProgressNotice } from "./ProgressNotice";

export function LoadError({
  error,
  retry,
}: {
  error: string;
  retry?: () => void;
}) {
  return error ? (
    <Alert
      severity="error"
      action={
        retry ? (
          <Button color="inherit" size="small" onClick={retry}>
            重试
          </Button>
        ) : undefined
      }
    >
      {error}
    </Alert>
  ) : null;
}

export const inlineCodeSx = (theme: Theme) => ({
  mx: 0.5,
  px: 0.65,
  py: "2px",
  borderRadius: 1,
  fontFamily: `"Cascadia Mono", "SFMono-Regular", Consolas, "Liberation Mono", ${theme.typography.fontFamily}`,
  fontSize: "0.96em",
  fontWeight: 500,
  whiteSpace: "nowrap",
  color: "text.primary",
  background: `linear-gradient(135deg, ${alpha(theme.palette.primary.main, 0.16)}, ${alpha(theme.palette.primary.main, 0.04)})`,
  boxShadow: `inset 0 1px 0 ${alpha(theme.palette.primary.main, 0.2)}`,
});

export function IconAction({
  label,
  tooltip = label,
  onClick,
  children,
  disabled = false,
}: {
  label: string;
  tooltip?: string;
  onClick?: () => void;
  children: ReactNode;
  disabled?: boolean;
}) {
  return (
    <Tooltip title={tooltip}>
      <span>
        <IconButton
          size="small"
          aria-label={label}
          onClick={(e) => {
            e.stopPropagation();
            onClick?.();
          }}
          disabled={disabled}
        >
          {children}
        </IconButton>
      </span>
    </Tooltip>
  );
}
export function PageTitle({
  left,
  right,
}: {
  left?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: "1fr auto 1fr",
        alignItems: "center",
        width: "100%",
        px: { mobile: 1, laptop: 2 },
        mb: 2,
        flexShrink: 0,
        minHeight: { mobile: 84, tablet: 56 },
      }}
    >
      <Box sx={{ justifySelf: "start", display: "flex", gap: 0.5 }}>{left}</Box>
      <Typography
        component="h1"
        variant="h4"
        fontWeight="bold"
        textAlign="center"
        sx={{ fontSize: { mobile: "clamp(20px, 6vw, 28px)", tablet: 34 } }}
      >
        <span className="app-wordmark">hifi-music-nano</span>
      </Typography>
      <Box sx={{ justifySelf: "end", display: "flex", gap: 0.5 }}>{right}</Box>
    </Box>
  );
}
const emptyIcons = {
  artists: UserRound,
  albums: Disc3,
  tracks: AudioLines,
  inbox: InboxIcon,
  history: History,
  folder: FolderOpen,
  details: FileMusic,
  search: SearchX,
};

export function Empty({
  children,
  kind = "search",
  accent = "primary",
  loading = false,
  fill = false,
}: {
  children: ReactNode;
  kind?: keyof typeof emptyIcons;
  accent?: "primary" | "secondary";
  loading?: boolean;
  fill?: boolean;
}) {
  const Icon = emptyIcons[kind];
  useProgressNotice(
    loading,
    typeof children === "string" ? children : "加载中",
  );
  if (loading) return null;
  return (
    <Box
      className="empty-state"
      data-empty-kind={kind}
      aria-busy={loading || undefined}
      sx={{
        minHeight: 184,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        flex: fill ? 1 : undefined,
        minWidth: 0,
        gap: 1.75,
        px: 3,
        py: 4,
        textAlign: "center",
      }}
    >
      <Box
        className="empty-state-icon"
        aria-hidden
        sx={(theme) => ({
          display: "grid",
          placeItems: "center",
          width: 64,
          height: 64,
          flexShrink: 0,
          borderRadius: "50%",
          color: alpha(theme.palette[accent].light, 0.75),
          background: `linear-gradient(145deg, ${alpha(theme.palette[accent].main, 0.09)}, ${alpha(theme.palette[accent].main, 0.025)})`,
        })}
      >
        <Icon size={30} strokeWidth={1.5} />
      </Box>
      <Typography
        variant="body2"
        color="text.secondary"
        sx={{ maxWidth: 320, lineHeight: 1.65, overflowWrap: "anywhere" }}
      >
        {children}
      </Typography>
    </Box>
  );
}
export function Modal({
  title,
  open,
  onClose,
  children,
  actions,
}: {
  title: ReactNode;
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  actions?: ReactNode;
}) {
  const titleID = useId();
  return (
    <Dialog
      open={open}
      onClose={onClose}
      aria-labelledby={titleID}
      fullWidth
      maxWidth="tablet"
    >
      <DialogTitle
        id={titleID}
        sx={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 2,
          fontSize: 18,
          overflowWrap: "anywhere",
        }}
      >
        <Box component="span" sx={{ minWidth: 0, flex: 1 }}>
          {title}
        </Box>
        <IconAction label="关闭" onClick={onClose}>
          <X size={18} />
        </IconAction>
      </DialogTitle>
      <DialogContent dividers>{children}</DialogContent>
      {actions ? <DialogActions sx={{ p: 2 }}>{actions}</DialogActions> : null}
    </Dialog>
  );
}
export function Confirm({
  title,
  text,
  onConfirm,
  children,
  color = "primary",
  onBusyChange,
}: {
  title: string;
  text: string;
  onConfirm: () => Promise<unknown>;
  children: (open: () => void) => ReactNode;
  color?: "primary" | "secondary" | "error";
  onBusyChange?: (busy: boolean) => void;
}) {
  const [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  return (
    <>
      {children(() => {
        setError("");
        setOpen(true);
      })}
      <Modal
        title={title}
        open={open}
        onClose={() => {
          if (!busy) setOpen(false);
        }}
        actions={
          <>
            <Button onClick={() => setOpen(false)} disabled={busy}>
              取消
            </Button>
            <Button
              color={color}
              variant="contained"
              disabled={busy}
              onClick={async () => {
                if (busy) return;
                setBusy(true);
                onBusyChange?.(true);
                try {
                  await onConfirm();
                  setOpen(false);
                } catch (e) {
                  setError(e instanceof Error ? e.message : "操作失败");
                } finally {
                  setBusy(false);
                  onBusyChange?.(false);
                }
              }}
            >
              {title}
            </Button>
          </>
        }
      >
        <Typography>{text}</Typography>
        {error ? (
          <Typography color="error" sx={{ mt: 2 }}>
            {error}
          </Typography>
        ) : null}
      </Modal>
    </>
  );
}
export function OperationIcon({ kind }: { kind: string }) {
  const Icon =
    kind === "edit"
      ? Tag
      : kind === "import"
        ? FileMusic
        : kind === "draft-delete" || kind === "delete"
          ? Trash2
          : kind === "artist-edit"
            ? UserRound
            : kind === "archive"
              ? InboxIcon
              : History;
  return <Icon size={16} />;
}
