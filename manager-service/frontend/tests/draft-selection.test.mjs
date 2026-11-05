import test from "node:test";
import assert from "node:assert/strict";
import {
  draftFileKey,
  draftSelection,
  pruneDraftSelection,
  toggleDraftFiles,
} from "../src/draft-selection.ts";

const batches = [
  { id: "album", files: [{ id: "one" }, { id: "two" }, { id: "three" }] },
  { id: "other", files: [{ id: "one" }] },
];

test("song choices stay isolated by folder, and partial folders select or clear all children", () => {
  let selected = toggleDraftFiles(new Set(), "album", ["two"]);
  assert.deepEqual(
    draftSelection(batches, selected).map(({ batch, files }) => [
      batch.id,
      files.map((file) => file.id),
    ]),
    [["album", ["two"]]],
  );
  selected = toggleDraftFiles(selected, "album", ["one", "two", "three"]);
  assert.equal(selected.size, 3);
  selected = toggleDraftFiles(selected, "other", ["one"]);
  selected = toggleDraftFiles(selected, "album", ["two"]);
  assert.equal(selected.has(draftFileKey("album", "two")), false);
  assert.equal(selected.has(draftFileKey("other", "one")), true);
  selected = toggleDraftFiles(selected, "album", ["one", "two", "three"]);
  selected = toggleDraftFiles(selected, "album", ["one", "two", "three"]);
  assert.deepEqual([...selected], [draftFileKey("other", "one")]);
});

test("imports, deletions and regrouping discard stale choices without selecting new uploads", () => {
  const selected = new Set([
    draftFileKey("album", "one"),
    draftFileKey("album", "two"),
  ]);
  assert.equal(pruneDraftSelection(batches, selected), selected);
  const remaining = [{ id: "album", files: [{ id: "two" }, { id: "new" }] }];
  assert.deepEqual(
    [...pruneDraftSelection(remaining, selected)],
    [draftFileKey("album", "two")],
  );
  assert.deepEqual(draftSelection([], selected), []);
  assert.equal(pruneDraftSelection([], selected).size, 0);
});
