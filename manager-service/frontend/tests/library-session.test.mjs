import test from "node:test";
import assert from "node:assert/strict";
import { createLibraryClient, libraryMediaURL } from "../src/http-client.ts";

test("a stale view cannot redirect its action to the current disk", async (t) => {
  let session = "first",
    requests = 0;
  const { request } = createLibraryClient(
    "first",
    () => session,
    () => "csrf",
  );
  t.mock.method(globalThis, "fetch", async (_url, options) => {
    requests++;
    assert.equal(options.headers["X-Library-Session"], "first");
    return Response.json({ ok: true });
  });
  assert.deepEqual(await request("tracks/edit", {}), { ok: true });
  session = "second";
  await assert.rejects(request("tracks/delete", {}), { name: "AbortError" });
  assert.equal(requests, 1);
  // Selecting a disk must remain available while no library is open.
  const { request: global } = createLibraryClient(
    "",
    () => "",
    () => "csrf",
  );
  t.mock.method(globalThis, "fetch", async (_url, options) => {
    assert.equal(options.headers["X-Library-Session"], undefined);
    return Response.json({ enabled: true });
  });
  assert.deepEqual(await global("system/disks"), { enabled: true });
});

test("an in-flight response from the previous library is discarded", async (t) => {
  let session = "first",
    respond;
  t.mock.method(
    globalThis,
    "fetch",
    () =>
      new Promise((resolve) => {
        respond = resolve;
      }),
  );
  const { request } = createLibraryClient(
    "first",
    () => session,
    () => "csrf",
  );
  const pending = request("tracks");
  session = "reopened-same-disk";
  respond(Response.json({ tracks: ["old"] }));
  await assert.rejects(pending, { name: "AbortError" });
});

test("native media carries the same ephemeral library identity", () => {
  assert.equal(
    libraryMediaURL("/api/tracks/one/audio", "a b"),
    "/api/tracks/one/audio?library_session=a%20b",
  );
  assert.equal(
    libraryMediaURL("/api/tracks/one/cover?v=2", "new"),
    "/api/tracks/one/cover?v=2&library_session=new",
  );
});

test("conditional queries bind their cached snapshot to the library session", async (t) => {
  let session = "one";
  const { query } = createLibraryClient(
    "one",
    () => session,
    () => "csrf",
  );
  t.mock.method(globalThis, "fetch", async (_url, options) => {
    assert.equal(options.headers["If-None-Match"], '"workspace"');
    assert.equal(options.headers["X-Library-Session"], "one");
    session = "two";
    return new Response(null, { status: 304 });
  });
  await assert.rejects(query("drafts", { etag: '"workspace"' }), {
    name: "AbortError",
  });
});
