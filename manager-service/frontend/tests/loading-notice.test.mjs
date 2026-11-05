import assert from "node:assert/strict";
import test from "node:test";
import { createLoadingNotice } from "../src/loading-notice.ts";

function setup() {
  let now = 0,
    sequence = 0;
  const timers = new Map(),
    events = [];
  const notice = createLoadingNotice(
    (action, content) => events.push({ action, ...content, time: now }),
    {
      schedule: (callback, delay) => {
        timers.set(++sequence, { callback, at: now + delay });
        return sequence;
      },
      cancel: (id) => timers.delete(id),
    },
  );
  function tick(time) {
    while (true) {
      const next = [...timers]
        .filter(([, timer]) => timer.at <= time)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) break;
      now = next[1].at;
      timers.delete(next[0]);
      next[1].callback();
    }
    now = time;
  }
  return { notice, events, timers, tick };
}

const content = { label: "加载曲库" };

test("fast page queries never create an entering or closing toast", () => {
  const s = setup();
  s.notice.set(true, content);
  s.tick(299);
  s.notice.set(false, content);
  s.tick(2000);
  assert.deepEqual(s.events, []);
  assert.equal(s.timers.size, 0);
});

test("a query finishing during entry hands retirement to the center without dismissing it", () => {
  const s = setup();
  s.notice.set(true, content);
  s.tick(300);
  assert.equal(s.events[0].action, "start");
  s.tick(310);
  s.notice.set(false, content);
  s.tick(2000);
  assert.deepEqual(
    s.events.map((e) => [e.action, e.time]),
    [
      ["start", 300],
      ["finish", 310],
    ],
  );
});

test("a long query finishes without taking over the center's reading timer", () => {
  const s = setup();
  s.notice.set(true, content);
  s.tick(1500);
  s.notice.set(false, content);
  assert.equal(s.events.at(-1).time, 1500);
  assert.equal(s.events.at(-1).action, "finish");
  assert.equal(s.timers.size, 0);
});

test("content changes do not postpone entry and a later request starts its own cycle", () => {
  const s = setup();
  s.notice.set(true, content);
  s.tick(200);
  s.notice.set(true, { label: "搜索曲库", detail: "新查询" });
  s.tick(300);
  assert.equal(s.events[0].label, "搜索曲库");
  s.tick(350);
  s.notice.set(false, content);
  s.tick(400);
  s.notice.set(true, content);
  s.tick(900);
  assert.deepEqual(
    s.events.map((e) => e.action),
    ["start", "finish", "start"],
  );
  s.notice.set(false, content);
  assert.equal(s.events.at(-1).action, "finish");
});

test("unmount cancels delayed entry and setup can safely run again", () => {
  const s = setup();
  s.notice.set(true, content);
  s.notice.dispose();
  s.tick(1000);
  assert.deepEqual(s.events, []);
  s.notice.set(true, content);
  s.tick(1300);
  assert.equal(s.events[0].action, "start");
  s.notice.dispose();
  s.notice.dispose();
  assert.deepEqual(
    s.events.map((e) => e.action),
    ["start", "release"],
  );
  assert.equal(s.timers.size, 0);
});

test("separate queries keep independent delay and cleanup lifecycles", () => {
  const a = setup(),
    b = setup();
  a.notice.set(true, content);
  b.notice.set(true, content);
  a.notice.dispose();
  b.tick(300);
  assert.equal(a.events.length, 0);
  assert.equal(b.events[0].action, "start");
  b.notice.dispose();
});
