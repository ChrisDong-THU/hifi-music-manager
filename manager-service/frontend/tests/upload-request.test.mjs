import assert from "node:assert/strict";
import test from "node:test";
import { uploadFile } from "../src/upload-request.ts";

function setup(t, signal) {
  let xhr;
  class FakeXHR {
    upload = {};
    headers = {};
    aborted = false;
    constructor() {
      xhr = this;
    }
    open(method, url) {
      this.method = method;
      this.url = url;
    }
    setRequestHeader(name, value) {
      this.headers[name] = value;
    }
    send(file) {
      this.file = file;
    }
    abort() {
      this.aborted = true;
      this.onabort?.();
    }
  }
  const original = globalThis.XMLHttpRequest;
  globalThis.XMLHttpRequest = FakeXHR;
  t.after(() => {
    globalThis.XMLHttpRequest = original;
  });
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const file = new File(["audio"], "one.flac");
  const fractions = [];
  const pending = uploadFile(
    "drafts/one/files?path=one.flac",
    file,
    "session",
    (n) => fractions.push(n),
    signal,
    "library-open",
  );
  return { xhr, pending, fractions, file };
}

test("upload progress extends the inactivity budget; validation has its own budget", async (t) => {
  const { xhr, pending, fractions, file } = setup(t);
  assert.equal(xhr.url, "/api/drafts/one/files?path=one.flac");
  assert.equal(xhr.headers["X-CSRF-Token"], "session");
  assert.equal(xhr.headers["X-Library-Session"], "library-open");
  assert.equal(xhr.file, file);
  for (let i = 0; i < 4; i++) {
    t.mock.timers.tick(100_000);
    xhr.upload.onprogress({ lengthComputable: true, loaded: i, total: 4 });
    assert.equal(xhr.aborted, false);
  }
  xhr.upload.onload();
  t.mock.timers.tick(299_999);
  assert.equal(xhr.aborted, false);
  xhr.status = 200;
  xhr.onload();
  await pending;
  t.mock.timers.tick(1_000_000);
  assert.equal(xhr.aborted, false, "success must dispose the timeout");
  assert.deepEqual(fractions, [0, 0.25, 0.5, 0.75]);
});

test("a stalled upload aborts and becomes retryable", async (t) => {
  const { xhr, pending } = setup(t);
  const rejected = assert.rejects(pending, /超时，可重试/);
  t.mock.timers.tick(120_000);
  await rejected;
  assert.equal(xhr.aborted, true);
});

test("malformed server errors have a readable fallback and release timers", async (t) => {
  const { xhr, pending } = setup(t);
  xhr.status = 502;
  xhr.responseText = "null";
  xhr.onload();
  await assert.rejects(pending, /上传失败，可重试/);
  t.mock.timers.tick(1_000_000);
  assert.equal(xhr.aborted, false);
});

test("network failures release the request lifecycle", async (t) => {
  const { xhr, pending } = setup(t);
  xhr.onerror();
  await assert.rejects(pending, /连接中断，可重试/);
  assert.equal(xhr.onload, null);
  assert.equal(xhr.upload.onprogress, null);
});

test("unmount aborts the current upload and removes progress handlers and timers", async (t) => {
  const controller = new AbortController();
  const { xhr, pending } = setup(t, controller.signal);
  const rejected = assert.rejects(pending, { name: "AbortError" });
  controller.abort();
  await rejected;
  assert.equal(xhr.aborted, true);
  assert.equal(xhr.upload.onprogress, null);
  assert.equal(xhr.onload, null);
  t.mock.timers.tick(1_000_000);
});
