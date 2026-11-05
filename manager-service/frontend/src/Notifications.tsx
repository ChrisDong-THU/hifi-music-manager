import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { Collapse, Slide, useMediaQuery } from "@mui/material";
import { X } from "lucide-react";
import { NoticeStatusIcon } from "./NoticeStatusIcon";
import {
  initialNoticeState,
  noticeDuration,
  noticeRevision,
  reduceNotices,
  type Notice,
  type NoticeData,
  type NoticeAction,
} from "./notice-state";
import { taskMessage, progressCountSpace } from "./task-labels";
import { createNoticeTimer, noticeTimings } from "./notice-timing";

type Presentation = {
  content?: ReactNode;
  onClose?: () => void;
  closeLabel?: string;
};
type Input = Omit<NoticeData, "id"> & Presentation;
type Center = {
  begin: (label: string, detail?: string) => string;
  start: (id: string, input: Input) => void;
  update: (id: string, input: Input) => void;
  dismiss: (id: string) => void;
  release: (id: string) => void;
  notify: (label: string, error?: boolean) => void;
};
const NotificationContext = createContext<Center | null>(null);
type Toast = Notice & Presentation;

function NoticeMessage({ notice }: { notice: Toast }) {
  return (
    <div
      className="notice-message"
      role={notice.status === "error" ? "alert" : "status"}
      aria-label={notice.label}
      aria-atomic="true"
    >
      <NoticeStatusIcon status={notice.status} />
      {notice.operation ? (
        <>
          <span
            className="notice-message-layer notice-layer-active"
            data-visible={notice.status === "active"}
            aria-hidden={notice.status !== "active"}
          >
            <span className="notice-label">
              {taskMessage(notice.operation, "active")}
            </span>
          </span>
          <span
            className="notice-message-layer notice-layer-success"
            data-visible={notice.status === "success"}
            aria-hidden={notice.status !== "success"}
          >
            <span className="notice-label">
              {taskMessage(notice.operation, "success")}
            </span>
          </span>
          {notice.status === "error" ? (
            <span className="notice-message-layer" data-visible="true">
              <span className="notice-label">{notice.label}</span>
            </span>
          ) : null}
        </>
      ) : (
        <span className="notice-message-layer" data-visible="true">
          <span className="notice-label">{notice.label}</span>
        </span>
      )}
    </div>
  );
}

function NoticeCard({
  notice,
  dismiss,
  expire,
  exited,
}: {
  notice: Toast;
  dismiss: Center["dismiss"];
  expire: (id: string, generation: number, revision: string) => void;
  exited: (id: string, generation: number) => void;
}) {
  const reduced = useMediaQuery("(prefers-reduced-motion: reduce)");
  const [expanded, setExpanded] = useState(true);
  const appeared = useRef(false);
  const [entered, setEntered] = useState(false);
  const timer = useMemo(
    () =>
      createNoticeTimer((revision) =>
        expire(notice.id, notice.generation, revision),
      ),
    [expire, notice.generation, notice.id],
  );
  useEffect(() => {
    // A fast request can finish before its first paint. There is no enter/exit
    // transition to wait for in that case, so acknowledge it directly.
    if (notice.closing && !appeared.current)
      exited(notice.id, notice.generation);
  }, [exited, notice.closing, notice.generation, notice.id]);
  const duration = noticeDuration(notice);
  // Presentation time starts after entry, and terminal transitions get their
  // own settling allowance. Polls cannot consume or restart the reading budget.
  useEffect(() => {
    timer.pause("entry", !entered);
    timer.pause("closing", notice.closing);
    timer.configure(
      noticeRevision(notice),
      duration === null
        ? null
        : duration +
            (notice.status === "success" && !reduced
              ? noticeTimings.settle
              : 0),
    );
  }, [
    duration,
    entered,
    notice.closing,
    notice.count,
    notice.detail,
    notice.finishedAt,
    notice.label,
    notice.status,
    reduced,
    timer,
  ]);
  useEffect(() => {
    const visibility = () => timer.pause("hidden", document.hidden);
    const blur = () => timer.pause("window", true);
    const focus = () => timer.pause("window", false);
    visibility();
    timer.pause("window", !document.hasFocus());
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("blur", blur);
    window.addEventListener("focus", focus);
    return () => {
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("blur", blur);
      window.removeEventListener("focus", focus);
      timer.dispose();
    };
  }, [timer]);
  const unknown = notice.status === "active" && notice.value === undefined;
  const count = notice.count || `${Math.round(notice.value ?? 0)}%`;
  return (
    <Collapse
      className="notice-slot"
      in={expanded}
      timeout={reduced ? 0 : noticeTimings.collapse}
      unmountOnExit
      onExited={() => exited(notice.id, notice.generation)}
    >
      <div className="notice-gap">
        <Slide
          direction="left"
          in={!notice.closing}
          appear
          timeout={
            reduced
              ? 0
              : {
                  enter: noticeTimings.enter,
                  exit: noticeTimings.exit,
                }
          }
          easing={{
            enter: "cubic-bezier(0.16, 1, 0.3, 1)",
            exit: "cubic-bezier(0.4, 0, 1, 1)",
          }}
          onEnter={() => {
            appeared.current = true;
          }}
          onEntered={() => setEntered(true)}
          onExited={() => setExpanded(false)}
        >
          <section
            className="notice-toast"
            data-status={notice.status}
            data-closing={notice.closing}
            data-notice-id={notice.id}
            data-generation={notice.generation}
            aria-label={notice.label}
            onPointerEnter={(event) => {
              if (event.pointerType === "mouse") timer.pause("hover", true);
            }}
            onPointerLeave={() => timer.pause("hover", false)}
            onFocusCapture={() => timer.pause("focus", true)}
            onBlurCapture={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget))
                timer.pause("focus", false);
            }}
          >
            <div className="notice-main">
              <NoticeMessage notice={notice} />
              {notice.hasProgress ? (
                <span className="notice-count">
                  <span className="notice-count-space" aria-hidden="true">
                    {progressCountSpace(count)}
                  </span>
                  <span className="notice-count-value">{count}</span>
                </span>
              ) : null}
              <button
                type="button"
                className="notice-close"
                aria-label={notice.closeLabel || "关闭消息"}
                disabled={notice.closing}
                tabIndex={notice.closing ? -1 : 0}
                onClick={() => dismiss(notice.id)}
              >
                <X size={16} />
              </button>
            </div>
            {notice.content && !notice.released ? (
              <div className="notice-content">{notice.content}</div>
            ) : null}
            <div
              className="notice-progress"
              data-unknown={unknown}
              role={notice.status === "active" ? "progressbar" : undefined}
              aria-label={
                notice.status === "active" ? notice.label + "进度" : undefined
              }
              aria-valuemin={notice.status === "active" ? 0 : undefined}
              aria-valuemax={notice.status === "active" ? 100 : undefined}
              aria-valuenow={
                notice.status === "active" && !unknown
                  ? Math.round(notice.value ?? 0)
                  : undefined
              }
            >
              <div
                className="notice-progress-fill"
                style={{
                  transform: `scaleX(${unknown ? 0 : (notice.value ?? 0) / 100})`,
                }}
              />
              <div className="notice-progress-sweep" />
            </div>
          </section>
        </Slide>
      </div>
    </Collapse>
  );
}

export function NotificationsProvider({ children }: { children: ReactNode }) {
  const store = useRef(initialNoticeState());
  const [state, setState] = useState(store.current);
  const sequence = useRef(0);
  const callbacks = useRef(new Map<string, () => void>());
  const recent = useRef<{ label: string; error: boolean; time: number } | null>(
    null,
  );
  // Synchronous commits make auto-close, unmount and late updates share one
  // lifecycle. Only the transition's onExited callback removes visible records.
  const send = useCallback((action: NoticeAction) => {
    const next = reduceNotices(store.current, action);
    if (next === store.current) return false;
    store.current = next;
    setState(next);
    return true;
  }, []);
  const publish = useCallback(
    (type: "start" | "update", id: string, input: Input) => {
      const { onClose, ...data } = input;
      if (!send({ type, data: { ...data, id }, now: Date.now() })) return;
      if (onClose) callbacks.current.set(id, onClose);
      else callbacks.current.delete(id);
      if (input.status !== "active")
        recent.current = {
          label: input.label,
          error: input.status === "error",
          time: Date.now(),
        };
    },
    [send],
  );
  const start = useCallback<Center["start"]>(
    (id, input) => publish("start", id, input),
    [publish],
  );
  const update = useCallback<Center["update"]>(
    (id, input) => publish("update", id, input),
    [publish],
  );
  const dismiss = useCallback(
    (id: string) => {
      if (!send({ type: "dismiss", id })) return;
      const callback = callbacks.current.get(id);
      callbacks.current.delete(id);
      callback?.();
    },
    [send],
  );
  const release = useCallback(
    (id: string) => {
      callbacks.current.delete(id);
      send({ type: "release", id });
    },
    [send],
  );
  const expire = useCallback(
    (id: string, generation: number, revision: string) => {
      if (!send({ type: "expire", id, generation, revision })) return;
      const callback = callbacks.current.get(id);
      callbacks.current.delete(id);
      callback?.();
    },
    [send],
  );
  const exited = useCallback(
    (id: string, generation: number) => {
      send({ type: "exited", id, generation });
    },
    [send],
  );
  const begin = useCallback(
    (label: string, detail?: string) => {
      const id = `task-${++sequence.current}`;
      start(id, { label, detail, status: "active" });
      return id;
    },
    [start],
  );
  const notify = useCallback(
    (label: string, error = false) => {
      const now = Date.now();
      if (
        recent.current &&
        recent.current.label === label &&
        recent.current.error === error &&
        now - recent.current.time < 1500
      )
        return;
      start(`message-${++sequence.current}`, {
        label,
        status: error ? "error" : "success",
      });
    },
    [start],
  );
  const center = useMemo(
    () => ({ begin, start, update, dismiss, release, notify }),
    [begin, start, update, dismiss, release, notify],
  );
  return (
    <NotificationContext.Provider value={center}>
      {children}
      {createPortal(
        <aside className="notice-center" aria-label="任务与消息">
          {state.notices.map((notice) => (
            <NoticeCard
              key={`${notice.id}:${notice.generation}`}
              notice={notice as Toast}
              dismiss={dismiss}
              expire={expire}
              exited={exited}
            />
          ))}
        </aside>,
        document.body,
      )}
    </NotificationContext.Provider>
  );
}

export function useNotifications() {
  const context = useContext(NotificationContext);
  if (!context) throw new Error("NotificationsProvider missing");
  return context;
}
