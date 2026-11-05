import test from "node:test";
import assert from "node:assert/strict";
import {
  updateNotice,
  noticeDuration,
  initialNoticeState,
  reduceNotices,
} from "../src/notice-state.ts";
import {
  taskKind,
  taskLabels,
  completedLabels,
  progressCountSpace,
} from "../src/task-labels.ts";

test("draft deletion has its own paired messages without changing library deletion", () => {
  assert.equal(taskKind("drafts/example/delete"), "draft-delete");
  assert.equal(taskKind("tracks/delete"), "delete");
  assert.equal(taskLabels["draft-delete"], "正在删除草稿");
  assert.equal(completedLabels["draft-delete"], "完成删除草稿");
});

test("progress becomes a completion in the same toast and keeps outgoing details for the transition", () => {
  let notices = updateNotice(
    [],
    { id: "upload", label: "读取上传文件", status: "active" },
    100,
  );
  assert.equal(notices[0].value, undefined);
  assert.equal(noticeDuration(notices[0]), null);
  notices = updateNotice(
    notices,
    {
      id: "upload",
      label: "上传音乐",
      status: "active",
      value: 42,
      detail: "专辑/歌曲.flac",
      count: "2/5",
    },
    200,
  );
  notices = updateNotice(
    notices,
    { id: "upload", label: "上传完成", status: "success" },
    300,
  );
  assert.equal(notices.length, 1);
  assert.equal(notices[0].createdAt, 100);
  assert.equal(notices[0].finishedAt, 300);
  assert.equal(notices[0].value, 100);
  assert.equal(notices[0].detail, "专辑/歌曲.flac");
  assert.equal(notices[0].count, "2/5");
  assert.equal(noticeDuration(notices[0]), 3500);
  notices = updateNotice(
    notices,
    { id: "upload", label: "上传完成", status: "success" },
    900,
  );
  assert.equal(
    notices[0].finishedAt,
    300,
    "unchanged completion must not extend the auto-dismiss timer",
  );
});

test("failure persists for retry, and retry resets completion timing without replacing the toast", () => {
  let notices = updateNotice(
    [],
    {
      id: "one",
      label: "上传未完成",
      status: "error",
      persistent: true,
      value: 50,
    },
    10,
  );
  assert.equal(noticeDuration(notices[0]), null);
  notices = updateNotice(
    notices,
    { id: "one", label: "上传音乐", status: "active", value: 50 },
    20,
  );
  assert.equal(notices[0].finishedAt, undefined);
  notices = updateNotice(
    notices,
    { id: "one", label: "上传完成", status: "success" },
    30,
  );
  assert.equal(notices[0].createdAt, 10);
  assert.equal(notices[0].finishedAt, 30);
  assert.equal(noticeDuration(notices[0]), 3500);
  const error = updateNotice(
    [],
    { id: "error", label: "连接失败", status: "error" },
    40,
  )[0];
  assert.equal(noticeDuration(error), null);
});

test("concurrent tasks keep their own identity and percentage updates are bounded", () => {
  let notices = updateNotice(
    [],
    { id: "a", label: "扫描曲库", status: "active" },
    1,
  );
  notices = updateNotice(
    notices,
    { id: "b", label: "上传音乐", status: "active", value: -20 },
    2,
  );
  const original = notices[0];
  assert.equal(notices[1].value, 0);
  notices = updateNotice(
    notices,
    { id: "b", label: "上传音乐", status: "active", value: 120 },
    3,
  );
  assert.equal(notices[1].value, 100);
  assert.equal(notices[0], original);
  notices = updateNotice(
    notices,
    { id: "a", label: "扫描完成", status: "success" },
    4,
  );
  assert.deepEqual(
    notices.map((notice) => [notice.id, notice.status]),
    [
      ["a", "success"],
      ["b", "active"],
    ],
  );
});

test("queued actions use the correct shared task labels", () => {
  assert.equal(taskKind("scan"), "scan");
  assert.equal(taskKind("drafts/id/prepare"), "prepare");
  assert.equal(taskKind("drafts/id/import"), "import");
  assert.equal(taskKind("albums/edit"), "edit");
  assert.equal(taskKind("albums/delete"), "delete");
});

test("a closing message ignores late updates and is removed only after exit acknowledgement", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "a", label: "上传完成", status: "success" },
    now: 1,
  });
  const generation = state.notices[0].generation;
  state = reduceNotices(state, { type: "dismiss", id: "a" });
  assert.equal(
    state.notices.length,
    1,
    "keep the record throughout its exit animation",
  );
  assert.equal(state.notices[0].closing, true);
  assert.equal(
    reduceNotices(state, {
      type: "update",
      data: { id: "a", label: "上传完成", status: "success" },
      now: 2,
    }),
    state,
    "a parent rerender must not reopen a closing message",
  );
  state = reduceNotices(state, { type: "exited", id: "a", generation });
  assert.equal(state.notices.length, 0);
  assert.equal(
    reduceNotices(state, {
      type: "update",
      data: { id: "a", label: "上传完成", status: "success" },
      now: 3,
    }),
    state,
    "late effects must not resurrect an already removed message",
  );
});

test("a new request supersedes a closing one and stale exit callbacks cannot remove it", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "search", label: "搜索曲库", status: "active" },
    now: 1,
  });
  const oldGeneration = state.notices[0].generation;
  state = reduceNotices(state, { type: "dismiss", id: "search" });
  state = reduceNotices(state, {
    type: "start",
    data: { id: "search", label: "搜索曲库", status: "active" },
    now: 2,
  });
  const newGeneration = state.notices[0].generation;
  assert.ok(newGeneration > oldGeneration);
  assert.equal(state.notices[0].closing, false);
  assert.equal(
    reduceNotices(state, {
      type: "exited",
      id: "search",
      generation: oldGeneration,
    }),
    state,
  );
  state = reduceNotices(state, { type: "dismiss", id: "search" });
  assert.equal(
    reduceNotices(state, {
      type: "exited",
      id: "search",
      generation: oldGeneration,
    }),
    state,
  );
  state = reduceNotices(state, {
    type: "exited",
    id: "search",
    generation: newGeneration,
  });
  assert.equal(state.notices.length, 0);
});

test("manual close, auto-close and component cleanup are idempotent", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "a", label: "已完成", status: "success" },
    now: 1,
  });
  state = reduceNotices(state, { type: "dismiss", id: "a" });
  assert.equal(reduceNotices(state, { type: "dismiss", id: "a" }), state);
  assert.equal(reduceNotices(state, { type: "dismiss", id: "missing" }), state);
  const generation = state.notices[0].generation;
  state = reduceNotices(state, { type: "exited", id: "a", generation });
  assert.equal(
    reduceNotices(state, { type: "exited", id: "a", generation }),
    state,
  );
});

test("long sessions discard closed message records and ignore unknown updates", () => {
  let state = initialNoticeState();
  for (let i = 0; i < 1000; i++) {
    const id = `task-${i}`;
    state = reduceNotices(state, {
      type: "start",
      data: { id, label: "上传音乐", status: "active" },
      now: i,
    });
    const generation = state.notices[0].generation;
    state = reduceNotices(state, { type: "dismiss", id });
    state = reduceNotices(state, { type: "exited", id, generation });
  }
  assert.deepEqual(state, { notices: [], generation: 1000 });
  assert.equal(
    reduceNotices(state, {
      type: "update",
      data: { id: "task-0", label: "完成上传音乐", status: "success" },
      now: 1001,
    }),
    state,
  );
});

test("completion preserves generation and closing one task cannot alter another task", () => {
  let state = reduceNotices(initialNoticeState(), {
    type: "start",
    data: { id: "a", label: "上传音乐", status: "active", value: 50 },
    now: 1,
  });
  const generation = state.notices[0].generation;
  state = reduceNotices(state, {
    type: "update",
    data: { id: "a", label: "上传完成", status: "success" },
    now: 2,
  });
  assert.equal(state.notices[0].generation, generation);
  state = reduceNotices(state, {
    type: "start",
    data: { id: "b", label: "导入音乐", status: "active" },
    now: 3,
  });
  const other = state.notices[1];
  state = reduceNotices(state, { type: "dismiss", id: "a" });
  state = reduceNotices(state, { type: "exited", id: "a", generation });
  assert.deepEqual(state.notices, [other]);
});

test("every task uses equal-length active and complete messages for the same operation", () => {
  for (const kind of Object.keys(taskLabels)) {
    assert.ok(taskLabels[kind].startsWith("正在"));
    assert.ok(completedLabels[kind].startsWith("完成"));
    assert.equal(taskLabels[kind].slice(2), completedLabels[kind].slice(2));
    assert.equal(
      Array.from(taskLabels[kind]).length,
      Array.from(completedLabels[kind]).length,
    );
  }
  let notices = updateNotice(
    [],
    { id: "save", label: "保存歌手信息", status: "active" },
    1,
  );
  assert.equal(notices[0].label, "正在保存歌手信息");
  notices = updateNotice(
    notices,
    { id: "save", label: "歌手信息已保存", status: "success" },
    2,
  );
  assert.equal(notices[0].label, "完成保存歌手信息");
  assert.equal(notices[0].operation, "保存歌手信息");
});

test("known progress retains its column while unknown progress never gains a completion percentage", () => {
  let known = updateNotice(
    [],
    {
      id: "known",
      label: "上传音乐",
      status: "active",
      count: "0/120 · 0%",
      value: 0,
    },
    1,
  );
  assert.equal(known[0].hasProgress, true);
  known = updateNotice(
    known,
    {
      id: "known",
      label: "完成上传音乐",
      status: "success",
      count: "120/120 · 100%",
    },
    2,
  );
  assert.equal(known[0].hasProgress, true);
  assert.equal(known[0].count, "120/120 · 100%");
  assert.equal(known[0].label, "完成上传音乐");
  let unknown = updateNotice(
    [],
    { id: "unknown", label: "正在扫描曲库", status: "active" },
    1,
  );
  unknown = updateNotice(
    unknown,
    { id: "unknown", label: "完成扫描曲库", status: "success" },
    2,
  );
  assert.equal(unknown[0].hasProgress, false);
  assert.equal(unknown[0].label, "完成扫描曲库");
});

test("the numeric column reserves its final width from the first progress update", () => {
  assert.equal(progressCountSpace("0/120 · 0%"), "120/120 · 100%");
  assert.equal(progressCountSpace("99/120 · 83%"), "120/120 · 100%");
  assert.equal(progressCountSpace("120/120 · 100%"), "120/120 · 100%");
  assert.equal(progressCountSpace("2/9"), "9/9");
  assert.equal(progressCountSpace("4%"), "100%");
});
