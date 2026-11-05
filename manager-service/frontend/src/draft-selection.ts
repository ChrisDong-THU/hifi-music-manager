import type { Draft } from "./types";

export const draftFileKey = (batchID: string, fileID: string) =>
  `${batchID}:${fileID}`;

export function toggleDraftFiles(
  selected: ReadonlySet<string>,
  batchID: string,
  fileIDs: string[],
) {
  const keys = fileIDs.map((id) => draftFileKey(batchID, id));
  const clear = keys.every((key) => selected.has(key));
  const next = new Set(selected);
  for (const key of keys) clear ? next.delete(key) : next.add(key);
  return next;
}

export function draftSelection(
  batches: Draft[],
  selected: ReadonlySet<string>,
) {
  return batches.flatMap((batch) => {
    const files = batch.files.filter((file) =>
      selected.has(draftFileKey(batch.id, file.id)),
    );
    return files.length ? [{ batch, files }] : [];
  });
}

export function pruneDraftSelection(batches: Draft[], selected: Set<string>) {
  const available = new Set(
    batches.flatMap((batch) =>
      batch.files.map((file) => draftFileKey(batch.id, file.id)),
    ),
  );
  const next = new Set([...selected].filter((key) => available.has(key)));
  return next.size === selected.size ? selected : next;
}
