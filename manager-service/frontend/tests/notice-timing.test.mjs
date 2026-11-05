import test from "node:test";
import assert from "node:assert/strict";
import { createNoticeTimer, noticeTimings } from "../src/notice-timing.ts";
import { createLoadingNotice } from "../src/loading-notice.ts";
import {
  initialNoticeState,
  reduceNotices,
  noticeDuration,
  noticeRevision,
} from "../src/notice-state.ts";

function setup() {
  let now = 0,
    sequence = 0;
  const timers = new Map(),
    expired = [];
  const clock = {
    now: () => now,
    schedule(callback, delay) {
      timers.set(++sequence, { callback, at: now + delay });
      return sequence;
    },
    cancel: (id) => timers.delete(id),
  };
  const timer = createNoticeTimer(() => expired.push(now), clock);
  function tick(time) {
    while (true) {
      const next = [...timers]
        .filter(([, t]) => t.at <= time)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) break;
      now = next[1].at;
      timers.delete(next[0]);
      next[1].callback();
    }
    now = time;
  }
  return { timer, clock, timers, expired, tick };
}

test("fast completion before entry still gets the entire settled reading budget", () => {
  const s = setup();
  s.timer.pause("entry", true);
  s.timer.configure("success", noticeTimings.success + noticeTimings.settle);
  s.tick(20000); // first paint may be delayed by a busy main thread
  assert.deepEqual(s.expired, []);
  s.timer.pause("entry", false);
  s.tick(24019);
  assert.deepEqual(s.expired, []);
  s.tick(24020);
  assert.deepEqual(s.expired, [24020]);
});

test("overlapping hover, keyboard and background pauses preserve the remaining budget", () => {
  const s = setup();
  s.timer.configure("success", 3500);
  s.tick(1000);
  s.timer.pause("hover", true);
  s.timer.pause("focus", true);
  s.timer.pause("hidden", true);
  s.tick(10000);
  s.timer.pause("hover", false);
  s.timer.pause("hidden", false);
  s.tick(20000);
  assert.deepEqual(s.expired, []);
  s.timer.pause("focus", false);
  s.tick(22499);
  assert.deepEqual(s.expired, []);
  s.tick(22500);
  assert.deepEqual(s.expired, [22500]);
});

test("unchanged polls do not extend a completion but changed readable content gets a fresh budget", () => {
  const s = setup();
  s.timer.configure("completed A", 3500);
  s.tick(1000);
  s.timer.configure("completed A", 3500);
  s.tick(3000);
  s.timer.configure("completed B", 3500);
  s.tick(6499);
  assert.deepEqual(s.expired, []);
  s.tick(6500);
  assert.deepEqual(s.expired, [6500]);
  s.timer.configure("completed B", 3500);
  assert.equal(
    s.timers.size,
    0,
    "a rerender cannot schedule a second expiration",
  );
});

test("a retry and a disposed owner invalidate already queued timeout callbacks", () => {
  const s = setup();
  s.timer.configure("success", 3500);
  const stale = [...s.timers.values()][0].callback;
  s.timer.configure("active retry", null);
  stale();
  assert.deepEqual(s.expired, []);
  s.timer.configure("success retry", 3500);
  const afterUnmount = [...s.timers.values()][0].callback;
  s.timer.dispose();
  afterUnmount();
  s.tick(10000);
  assert.deepEqual(s.expired, []);
  assert.equal(s.timers.size, 0);
  s.timer.configure("success retry", 3500); // React effect cleanup/setup replay
  s.tick(13500);
  assert.deepEqual(s.expired, [13500]);
});

test("errors and running tasks have no automatic deadline", () => {
  const s = setup();
  s.timer.configure("error", null);
  s.tick(100000);
  assert.equal(s.timers.size, 0);
  assert.deepEqual(s.expired, []);
});

test("query unmount during entry cannot bypass the center's minimum readable time", () => {
  const s = setup();
  let state = initialNoticeState();
  s.timer.pause("entry", true);
  const loading = createLoadingNotice((action, content) => {
    state = reduceNotices(
      state,
      action === "release" || action === "finish"
        ? { type: "release", id: "query" }
        : {
            type: action,
            data: { ...content, id: "query", status: "active" },
            now: 0,
          },
    );
    s.timer.configure("query", noticeDuration(state.notices[0]));
  }, s.clock);
  loading.set(true, { label: "加载曲库" });
  s.tick(300);
  s.tick(310);
  loading.dispose();
  assert.equal(state.notices[0].closing, false);
  s.tick(620);
  s.timer.pause("entry", false);
  s.tick(2119);
  assert.deepEqual(s.expired, []);
  s.tick(2120);
  assert.deepEqual(s.expired, [2120]);
});

test("completion survives owner unmount; stale generations cannot expire a new cycle", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "upload", label: "上传音乐", status: "active" },
    now: 0,
  });
  const generation = state.notices[0].generation;
  state = reduceNotices(state, {
    type: "update",
    data: { id: "upload", label: "完成上传音乐", status: "success" },
    now: 10,
  });
  state = reduceNotices(state, { type: "release", id: "upload" });
  const revision = noticeRevision(state.notices[0]);
  assert.equal(state.notices[0].closing, false);
  assert.equal(noticeDuration(state.notices[0]), 3500);
  assert.equal(
    reduceNotices(state, {
      type: "update",
      data: { id: "upload", label: "迟到的更新", status: "active" },
      now: 20,
    }),
    state,
  );
  state = reduceNotices(state, {
    type: "start",
    data: { id: "upload", label: "上传音乐", status: "active" },
    now: 30,
  });
  assert.equal(
    reduceNotices(state, {
      type: "expire",
      id: "upload",
      generation,
      revision,
    }),
    state,
  );
  assert.equal(
    reduceNotices(state, {
      type: "expire",
      id: "upload",
      generation: state.notices[0].generation,
      revision: noticeRevision(state.notices[0]),
    }),
    state,
    "active tasks cannot auto-expire",
  );
  state = reduceNotices(state, {
    type: "update",
    data: { id: "upload", label: "完成上传音乐", status: "success" },
    now: 40,
  });
  state = reduceNotices(state, {
    type: "expire",
    id: "upload",
    generation: state.notices[0].generation,
    revision: noticeRevision(state.notices[0]),
  });
  assert.equal(state.notices[0].closing, true);
});

test("a timeout queued before a terminal update cannot close changed content", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "message", label: "第一项已完成", status: "success" },
    now: 0,
  });
  const stale = {
    type: "expire",
    id: "message",
    generation: state.notices[0].generation,
    revision: noticeRevision(state.notices[0]),
  };
  state = reduceNotices(state, {
    type: "update",
    data: { id: "message", label: "两项已完成", status: "success" },
    now: 3000,
  });
  assert.equal(reduceNotices(state, stale), state);
});

test("errors remain readable after owner cleanup until explicitly dismissed", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "error", label: "保存失败", status: "error" },
    now: 0,
  });
  state = reduceNotices(state, { type: "release", id: "error" });
  assert.equal(noticeDuration(state.notices[0]), null);
  assert.equal(state.notices[0].closing, false);
  state = reduceNotices(state, { type: "dismiss", id: "error" });
  assert.equal(state.notices[0].closing, true);
});
