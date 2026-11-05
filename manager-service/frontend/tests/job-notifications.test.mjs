import test from "node:test";
import assert from "node:assert/strict";
import { createJobNotifications } from "../src/job-notifications.ts";

function fixture() {
  const events = [];
  const reporter = createJobNotifications({
    begin(label) {
      events.push(["begin", "submission", label]);
      return "submission";
    },
    start(id, data) {
      events.push(["start", id, data]);
    },
    update(id, data) {
      events.push(["update", id, data]);
    },
    dismiss(id) {
      events.push(["dismiss", id]);
    },
  });
  return { reporter, events };
}
const job = {
  id: "one",
  kind: "import",
  state: "running",
  message: "saving",
  done: 0,
  total: 1,
};

test("unchanged progress does not invalidate the notification view", () => {
  const { reporter, events } = fixture();
  reporter.report([job]);
  const before = events.length;
  reporter.reportCurrent();
  reporter.report([{ ...job }]);
  assert.equal(events.length, before);
  reporter.report([{ ...job, done: 1 }]);
  assert.equal(events.length, before + 1);
});

test("polling that overtakes a POST still uses the original submission notice", async () => {
  const { reporter, events } = fixture();
  let respond;
  const pending = reporter.enqueue(
    "drafts/b/import",
    {},
    () =>
      new Promise((resolve) => {
        respond = resolve;
      }),
    async () => reporter.report([{ ...job, state: "done", done: 1 }]),
  );
  reporter.report([job]);
  assert.equal(events.filter((e) => e[0] === "start").length, 0);
  respond({ job: "one" });
  await pending;
  const completed = events.filter(
    (e) => e[0] === "update" && e[2].status === "success",
  );
  assert.equal(completed.length, 1);
  assert.equal(completed[0][1], "submission");
});

test("a lost POST response can rediscover the server job on a 304 poll", async () => {
  const { reporter, events } = fixture();
  let reject;
  const pending = reporter.enqueue(
    "drafts/b/import",
    {},
    () =>
      new Promise((_resolve, fail) => {
        reject = fail;
      }),
    async () => {},
  );
  reporter.report([job]);
  reject(new Error("connection lost"));
  await assert.rejects(pending, /connection lost/);
  reporter.reportCurrent();
  assert.equal(events.find((e) => e[0] === "start")[1], "job:one");
  reporter.switchScope([]);
  assert.ok(events.some((e) => e[0] === "dismiss" && e[1] === "job:one"));
});
