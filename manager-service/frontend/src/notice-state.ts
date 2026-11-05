import { taskMessage, taskOperation } from "./task-labels.ts";
import { noticeTimings } from "./notice-timing.ts";

export type NoticeStatus = "active" | "success" | "error";
export type NoticeData = {
  id: string;
  label: string;
  status: NoticeStatus;
  detail?: string;
  count?: string;
  value?: number;
  persistent?: boolean;
};
export type Notice = NoticeData & {
  createdAt: number;
  finishedAt?: number;
  closing: boolean;
  generation: number;
  operation?: string;
  hasProgress: boolean;
  released?: boolean;
};

export function updateNotice(
  previous: Notice[],
  data: NoticeData,
  now: number,
): Notice[] {
  const old = previous.find((notice) => notice.id === data.id);
  const operation =
    data.status === "active"
      ? taskOperation(data.label)
      : old?.operation ||
        (data.status === "success" && data.label.startsWith("完成")
          ? taskOperation(data.label)
          : undefined);
  const next: Notice = {
    ...data,
    label:
      operation && data.status !== "error"
        ? taskMessage(operation, data.status)
        : data.label,
    operation,
    hasProgress:
      data.status === "success"
        ? (old?.hasProgress ??
          (data.count !== undefined || data.value !== undefined))
        : data.count !== undefined || data.value !== undefined,
    value:
      data.status === "success"
        ? 100
        : data.value === undefined
          ? undefined
          : Math.max(0, Math.min(100, data.value)),
    // Keep the count column on completion so the same frame keeps its size.
    detail:
      data.status === "success" ? (data.detail ?? old?.detail) : data.detail,
    count: data.status === "success" ? (data.count ?? old?.count) : data.count,
    createdAt: old?.createdAt ?? now,
    finishedAt:
      data.status === "active"
        ? undefined
        : old?.status === data.status
          ? old.finishedAt
          : now,
    closing: false,
    generation: old?.generation ?? 0,
  };
  return old
    ? previous.map((notice) => (notice.id === data.id ? next : notice))
    : [...previous, next];
}

export function noticeDuration(notice: Notice) {
  return notice.status === "error" || notice.persistent
    ? null
    : notice.status === "active"
      ? notice.released
        ? noticeTimings.minimumVisible
        : null
      : noticeTimings.success;
}

export function noticeRevision(notice: Notice) {
  return JSON.stringify([
    notice.status,
    notice.finishedAt,
    notice.label,
    notice.detail,
    notice.count,
  ]);
}

export type NoticeState = {
  notices: Notice[];
  generation: number;
};
export type NoticeAction =
  | { type: "start"; data: NoticeData; now: number }
  | { type: "update"; data: NoticeData; now: number }
  | { type: "dismiss"; id: string }
  | { type: "release"; id: string }
  | { type: "expire"; id: string; generation: number; revision: string }
  | { type: "exited"; id: string; generation: number };

export function initialNoticeState(): NoticeState {
  return { notices: [], generation: 0 };
}

export function reduceNotices(
  state: NoticeState,
  action: NoticeAction,
): NoticeState {
  if (action.type === "start" || action.type === "update") {
    const old = state.notices.find((notice) => notice.id === action.data.id);
    // Only an explicit start can create a message. Late effects cannot revive
    // dismissed messages, without retaining every historical message ID.
    if (action.type === "update" && (!old || old.closing || old.released))
      return state;
    const newCycle = action.type === "start";
    const generation = newCycle ? state.generation + 1 : old!.generation;
    const previous = newCycle
      ? state.notices.filter((notice) => notice.id !== action.data.id)
      : state.notices;
    const notices = updateNotice(previous, action.data, action.now).map(
      (notice) =>
        notice.id === action.data.id ? { ...notice, generation } : notice,
    );
    return {
      notices,
      generation: Math.max(state.generation, generation),
    };
  }
  const notice = state.notices.find((item) => item.id === action.id);
  if (!notice) return state;
  if (action.type === "release") {
    if (notice.closing || notice.released) return state;
    return {
      ...state,
      notices: state.notices.map((item) =>
        item.id === action.id
          ? { ...item, released: true, content: undefined }
          : item,
      ),
    };
  }
  if (action.type === "dismiss" || action.type === "expire") {
    if (
      action.type === "expire" &&
      (notice.generation !== action.generation ||
        noticeRevision(notice) !== action.revision ||
        noticeDuration(notice) === null)
    )
      return state;
    if (notice.closing) return state;
    return {
      ...state,
      notices: state.notices.map((item) =>
        item.id === action.id ? { ...item, closing: true } : item,
      ),
    };
  }
  if (!notice.closing || notice.generation !== action.generation) return state;
  return {
    ...state,
    notices: state.notices.filter((item) => item.id !== action.id),
  };
}
