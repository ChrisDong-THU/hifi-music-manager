import test from "node:test";
import assert from "node:assert/strict";
import { apiFetch, jsonResponse } from "../src/http-client.ts";

test("GET supports cancellation and conditional polling without a request body", async (t) => {
  const controller = new AbortController();
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/state");
    assert.equal(options.method, "GET");
    assert.equal(options.body, undefined);
    assert.equal(options.headers["If-None-Match"], '"snapshot"');
    controller.abort();
    assert.equal(options.signal.aborted, true);
    return new Response(null, { status: 304 });
  });
  assert.equal(
    (
      await apiFetch("state", undefined, "", {
        signal: controller.signal,
        etag: '"snapshot"',
      })
    ).status,
    304,
  );
});

test("JSON mutations send their session token; multipart requests preserve browser boundaries", async (t) => {
  let request;
  t.mock.method(globalThis, "fetch", async (_path, options) => {
    request = options;
    return Response.json({ ok: true });
  });
  await apiFetch("tracks/edit", { ids: ["one"] }, "session", {
    ifMatch: '"editing-snapshot"',
  });
  assert.equal(request.headers["X-CSRF-Token"], "session");
  assert.equal(request.headers["If-Match"], '"editing-snapshot"');
  assert.equal(request.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(request.body), { ids: ["one"] });
  const form = new FormData();
  form.set("biography", "简介");
  await apiFetch("artists/edit", form, "session");
  assert.equal(request.headers["Content-Type"], undefined);
  assert.equal(request.body, form);
});

test("malformed success responses and API errors remain failures", async () => {
  await assert.rejects(jsonResponse(new Response("not JSON")), /响应格式无效/);
  await assert.rejects(
    jsonResponse(Response.json({ error: "标签无效" }, { status: 400 })),
    /标签无效/,
  );
  await assert.rejects(
    jsonResponse(Response.json(null, { status: 500 })),
    /请求失败/,
  );
  assert.deepEqual(await jsonResponse(Response.json({ ok: true })), {
    ok: true,
  });
});
