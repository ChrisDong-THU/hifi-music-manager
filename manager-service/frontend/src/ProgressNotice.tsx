import { useEffect, useId, useMemo, useRef, type ReactNode } from "react";
import { useNotifications } from "./Notifications";
import type { NoticeStatus } from "./notice-state";
import { createLoadingNotice } from "./loading-notice";

export function ProgressNotice({
  label,
  detail,
  count,
  value,
  status = "active",
  onClose,
  closeLabel,
  children,
}: {
  label: string;
  detail?: string;
  count?: string;
  value?: number;
  status?: NoticeStatus;
  onClose?: () => void;
  closeLabel?: string;
  children?: ReactNode;
}) {
  const id = useId();
  const { start, update, release } = useNotifications();
  const initialized = useRef(false);
  const previousStatus = useRef(status);
  const closeCallback = useRef(onClose);
  closeCallback.current = onClose;
  useEffect(() => {
    const publish =
      !initialized.current ||
      (status === "active" && previousStatus.current !== "active")
        ? start
        : update;
    initialized.current = true;
    previousStatus.current = status;
    publish(id, {
      label,
      detail,
      count,
      value,
      status,
      persistent: status === "error",
      content: children,
      onClose: () => closeCallback.current?.(),
      closeLabel,
    });
  }, [
    children,
    closeLabel,
    count,
    detail,
    id,
    label,
    status,
    start,
    update,
    value,
  ]);
  useEffect(
    () => () => {
      initialized.current = false;
      release(id);
    },
    [release, id],
  );
  return null;
}

export function useProgressNotice(
  active: boolean,
  label: string,
  detail?: string,
) {
  const id = useId();
  const { start, update, release } = useNotifications();
  const loadingNotice = useMemo(
    () =>
      createLoadingNotice((action, content) => {
        // A read ending (including abort or failure) is not a successful file
        // task. Retire it quietly without inventing a success result.
        if (action === "release" || action === "finish") release(id);
        else
          (action === "start" ? start : update)(id, {
            ...content,
            status: "active",
          });
      }),
    [release, id, start, update],
  );
  useEffect(() => {
    loadingNotice.set(active, { label, detail });
  }, [active, detail, label, loadingNotice]);
  useEffect(() => () => loadingNotice.dispose(), [loadingNotice]);
}
