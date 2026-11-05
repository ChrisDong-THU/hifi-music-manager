import test from "node:test";
import assert from "node:assert/strict";
import { draftTitle } from "../src/types.ts";

const tags = (patch = {}) => ({
  title: "旧歌名",
  artist: "旧歌手",
  album: "旧专辑",
  album_artist: "",
  genre: "",
  year: "",
  track: "",
  disc: "",
  lyrics: "",
  ...patch,
});
const batch = (paths, saved) => ({
  id: "batch",
  name: "原名称",
  state: "ready",
  files: paths.map((path, index) => ({ path, tags: saved?.[index] || tags() })),
  has_saved_tags: !!saved,
});

test("single file uses saved metadata and follows subsequent saves without changing paths", () => {
  const b = batch(
    ["uploads/batch/original.flac"],
    [
      tags({
        title: " 新歌名 ",
        artist: "新歌手",
        album: "新专辑",
        album_artist: "专辑歌手",
      }),
    ],
  );
  const before = structuredClone(b);
  assert.equal(draftTitle(b), "新歌名 - 新歌手[新专辑]");
  assert.deepEqual(b, before);
  b.files[0].tags.title = "再次保存";
  assert.equal(draftTitle(b), "再次保存 - 新歌手[新专辑]");
});

test("album folders, including one-track and grouped folders, prefer album artist", () => {
  const saved = tags({ album_artist: "专辑歌手" });
  assert.equal(
    draftTitle(batch(["uploads/batch/album/one.flac"], [saved])),
    "专辑歌手[旧专辑]",
  );
  const grouped = batch(["uploads/parent/album/CD1/one.flac"], [saved]);
  grouped.parent_id = "parent";
  assert.equal(draftTitle(grouped), "专辑歌手[旧专辑]");
  const b = batch(
    ["uploads/batch/one.flac", "uploads/batch/two.flac"],
    [saved, tags({ artist: "其他歌手", album_artist: "专辑歌手" })],
  );
  assert.equal(draftTitle(b), "专辑歌手[旧专辑]");
  assert.equal(
    draftTitle(batch(["uploads/batch/album/one.flac"])),
    "旧歌手[旧专辑]",
  );
});

test("empty or inconsistent metadata falls back to original name", () => {
  assert.equal(draftTitle(batch([], [])), "原名称");
  assert.equal(
    draftTitle(batch(["uploads/batch/one.flac"], [tags({ album: " " })])),
    "原名称",
  );
  assert.equal(
    draftTitle(batch(["uploads/batch/one.flac"], [tags({ title: "" })])),
    "原名称",
  );
  assert.equal(
    draftTitle(
      batch(
        ["uploads/batch/a/one.flac", "uploads/batch/a/two.flac"],
        [tags(), tags({ album: "其他专辑" })],
      ),
    ),
    "原名称",
  );
});

test("rereading source labels discards the previously saved display title", () => {
  const b = batch(["uploads/batch/original.flac"], [tags({ title: "已保存" })]);
  b.has_saved_tags = false;
  b.files[0].tags = tags();
  assert.equal(draftTitle(b), "旧歌名 - 旧歌手[旧专辑]");
  b.files[0].path = "uploads\\batch\\original.flac";
  assert.equal(draftTitle(b), "旧歌名 - 旧歌手[旧专辑]");
});
