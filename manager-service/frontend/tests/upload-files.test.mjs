import test from "node:test";
import assert from "node:assert/strict";
import { collectUploadFiles, hasUploadFiles } from "../src/upload-files.ts";

const audioFile = (name) => ({ name, webkitRelativePath: "" });
const fileEntry = (name) => ({
  isFile: true,
  isDirectory: false,
  name,
  file(resolve) {
    resolve(audioFile(name));
  },
});
function folder(name, pages) {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader() {
      let page = 0;
      return {
        readEntries(resolve) {
          resolve(pages[page++] || []);
        },
      };
    },
  };
}
const transfer = (entries) => ({
  types: ["Files"],
  files: [],
  items: entries.map((entry) => ({
    kind: "file",
    webkitGetAsEntry: () => entry,
    getAsFile: () => null,
  })),
});

test("directory drops read every page and preserve nested disc paths", async () => {
  const firstPage = Array.from({ length: 100 }, (_, i) =>
    fileEntry(`${i + 1}.flac`),
  );
  const directory = folder("专辑", [
    firstPage,
    [folder("CD2", [[fileEntry("101.MP3"), fileEntry("cover.png")]])],
  ]);
  const result = await collectUploadFiles(transfer([directory]));
  assert.equal(result.length, 101);
  assert.equal(result[0].path, "专辑/1.flac");
  assert.equal(result.at(-1).path, "专辑/CD2/101.MP3");
});

test("paste snapshots file items and keeps file fallbacks alongside directory entries", async () => {
  const source = transfer([folder("Album", [[fileEntry("song.flac")]])]);
  const file = audioFile("extra.wav");
  source.items.push({
    kind: "file",
    webkitGetAsEntry: () => null,
    getAsFile: () => file,
  });
  source.items.push({ kind: "string", getAsFile: () => null });
  const pending = collectUploadFiles(source);
  source.items.length = 0;
  const result = await pending;
  assert.deepEqual(
    result.map((item) => item.path),
    ["Album/song.flac", "extra.wav"],
  );
});

test("clipboard file lists work without entry support; text is not treated as upload content", async () => {
  const source = {
    types: ["Files"],
    items: [],
    files: [audioFile("one.opus"), audioFile("notes.txt")],
  };
  assert.equal(hasUploadFiles(source), true);
  assert.deepEqual(
    (await collectUploadFiles(source)).map((item) => item.path),
    ["one.opus"],
  );
  assert.equal(
    hasUploadFiles({
      types: ["text/plain"],
      files: [],
      items: [{ kind: "string" }],
    }),
    false,
  );
});

test("folder discovery stops at the upload limit and surfaces unreadable folders", async () => {
  const directory = folder("Large", [
    Array.from({ length: 301 }, (_, i) => fileEntry(`${i}.flac`)),
  ]);
  await assert.rejects(
    collectUploadFiles(transfer([directory])),
    /每批最多 300 首/,
  );
  const unreadable = {
    isDirectory: true,
    name: "Private",
    createReader: () => ({
      readEntries(resolve, reject) {
        reject(new Error("无法读取目录"));
      },
    }),
  };
  await assert.rejects(
    collectUploadFiles(transfer([unreadable])),
    /无法读取目录/,
  );
});

test("unmount during directory enumeration stops before reading returned children", async () => {
  const controller = new AbortController();
  let release;
  let reads = 0;
  const entry = fileEntry("song.flac");
  entry.file = () => {
    reads++;
  };
  const directory = {
    isDirectory: true,
    name: "Album",
    createReader: () => ({
      readEntries(resolve) {
        release = resolve;
      },
    }),
  };
  const pending = collectUploadFiles(transfer([directory]), controller.signal);
  controller.abort();
  release([entry]);
  await assert.rejects(pending, { name: "AbortError" });
  assert.equal(reads, 0);
});

test("cancellation while obtaining a file prevents traversing remaining entries", async () => {
  const controller = new AbortController();
  let release;
  let nextRead = false;
  const first = fileEntry("first.flac");
  first.file = (resolve) => {
    release = resolve;
  };
  const next = fileEntry("next.flac");
  next.file = () => {
    nextRead = true;
  };
  const pending = collectUploadFiles(
    transfer([first, next]),
    controller.signal,
  );
  controller.abort();
  release(audioFile("first.flac"));
  await assert.rejects(pending, { name: "AbortError" });
  assert.equal(nextRead, false);
});

test("cancelled discovery does not inspect transfer data or open unsupported files", async () => {
  const controller = new AbortController();
  controller.abort();
  const source = {
    get items() {
      throw new Error("transfer must not be inspected");
    },
  };
  await assert.rejects(collectUploadFiles(source, controller.signal), {
    name: "AbortError",
  });
  const artwork = fileEntry("cover.png");
  artwork.file = () => {
    throw new Error("unsupported file must not be opened");
  };
  assert.deepEqual(await collectUploadFiles(transfer([artwork])), []);
});
