import { noticeTimings } from "./notice-timing.ts";

type Content = { label: string; detail?: string };
type Action = "start" | "update" | "finish" | "release";
type Clock = {
  schedule: (
    callback: () => void,
    delay: number,
  ) => ReturnType<typeof setTimeout>;
  cancel: (timer: ReturnType<typeof setTimeout>) => void;
};

// Suppress fast reads. Once published, the notification center owns its reading
// time and completion, even if the query's component subsequently unmounts.
export function createLoadingNotice(
  publish: (action: Action, content: Content) => void,
  clock: Clock = {
    schedule: (callback, delay) => setTimeout(callback, delay),
    cancel: (timer) => clearTimeout(timer),
  },
) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let shown = false;
  let content: Content = { label: "" };
  function cancelTimer() {
    if (timer !== undefined) clock.cancel(timer);
    timer = undefined;
  }
  return {
    set(active: boolean, next: Content) {
      content = next;
      if (active) {
        if (shown) {
          cancelTimer();
          publish("update", content);
        } else if (timer === undefined) {
          timer = clock.schedule(() => {
            timer = undefined;
            shown = true;
            publish("start", content);
          }, noticeTimings.loadingDelay);
        }
      } else {
        cancelTimer();
        if (shown) {
          shown = false;
          publish("finish", content);
        }
      }
    },
    dispose() {
      cancelTimer();
      if (shown) {
        shown = false;
        publish("release", content);
      }
    },
  };
}
