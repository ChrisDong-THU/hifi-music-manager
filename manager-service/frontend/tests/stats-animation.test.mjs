import assert from "node:assert/strict";
import test from "node:test";
import { createStatsAnimation, emptyStats } from "../src/stats-animation.ts";

const target = {
  tracks: 24,
  artists: 4,
  albums: 8,
  bytes: 156346549,
  duration: 438.8933333333333,
};
function setup(initial = emptyStats) {
  let now = 0,
    id = 0;
  const frames = new Map(),
    values = [];
  const animation = createStatsAnimation(
    (value) => values.push(value),
    initial,
    {
      now: () => now,
      request: (callback) => {
        frames.set(++id, callback);
        return id;
      },
      cancel: (key) => frames.delete(key),
    },
  );
  function tick(time) {
    now = time;
    const pending = [...frames.values()];
    frames.clear();
    pending.forEach((callback) => callback(now));
  }
  return { animation, tick, frames, values };
}

test("a fast refresh still grows smoothly and settles on the exact final totals", () => {
  const s = setup(target);
  s.animation.reset();
  assert.deepEqual(s.values.at(-1), emptyStats);
  s.animation.to(target);
  s.tick(250);
  const middle = s.values.at(-1);
  for (const key of Object.keys(target))
    assert.ok(middle[key] > 0 && middle[key] < target[key]);
  s.tick(1000);
  assert.deepEqual(s.values.at(-1), target);
  assert.equal(s.frames.size, 0);
  const count = s.values.length;
  s.animation.to({ ...target });
  s.tick(2000);
  assert.equal(s.values.length, count);
});

test("live updates continue from the displayed value and identical polls do not restart animation", () => {
  const s = setup();
  s.animation.to(target);
  s.tick(250);
  const middle = s.values.at(-1);
  s.animation.to({ ...target });
  s.tick(1000);
  assert.deepEqual(s.values.at(-1), target);
  const next = { ...target, tracks: 30 };
  s.animation.to(next);
  s.tick(1250);
  assert.ok(
    s.values.at(-1).tracks > target.tracks &&
      s.values.at(-1).tracks < next.tracks,
  );
  assert.ok(s.values.at(-1).tracks > middle.tracks);
  s.tick(2000);
  assert.deepEqual(s.values.at(-1), next);
});

test("a new refresh and unmount cancel pending frames; reduced motion settles immediately", () => {
  const s = setup();
  s.animation.to(target);
  s.tick(200);
  s.animation.reset();
  s.tick(1000);
  assert.deepEqual(s.values.at(-1), emptyStats);
  s.animation.to(target);
  s.animation.to(target, 0);
  assert.deepEqual(s.values.at(-1), target);
  assert.equal(s.frames.size, 0);
  s.animation.to(emptyStats);
  s.animation.stop();
  s.tick(2000);
  assert.deepEqual(s.values.at(-1), target);
  assert.equal(s.frames.size, 0);
});

test("effect cleanup followed by setup resumes the same target", () => {
  const s = setup();
  s.animation.to(target);
  s.tick(200);
  const middle = s.values.at(-1).tracks;
  s.animation.stop();
  s.animation.to(target);
  s.tick(400);
  assert.ok(s.values.at(-1).tracks > middle);
  s.tick(1200);
  assert.deepEqual(s.values.at(-1), target);
});
