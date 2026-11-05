export type UploadItem = { file: File; path: string };

type FileEntry = {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  file: (
    resolve: (file: File) => void,
    reject: (error: unknown) => void,
  ) => void;
  createReader: () => {
    readEntries: (
      resolve: (entries: FileEntry[]) => void,
      reject: (error: unknown) => void,
    ) => void;
  };
};

type TransferItem = {
  kind: string;
  webkitGetAsEntry?: () => unknown;
  getAsFile: () => File | null;
};
type FileTransfer = {
  items: ArrayLike<TransferItem>;
  files: ArrayLike<File>;
  types: readonly string[];
};

export const isAudioFile = (path: string) =>
  /\.(flac|mp3|m4a|ogg|opus|wav|aiff|aif)$/i.test(path);

export function hasUploadFiles(transfer: FileTransfer) {
  return (
    transfer.types.includes("Files") ||
    transfer.files.length > 0 ||
    Array.from(transfer.items).some((item) => item.kind === "file")
  );
}

// Snapshot entries/files during the event: browsers protect DataTransfer after it returns.
export async function collectUploadFiles(
  transfer: FileTransfer,
  signal?: AbortSignal,
): Promise<UploadItem[]> {
  signal?.throwIfAborted();
  const items = Array.from(transfer.items)
    .filter((item) => item.kind === "file")
    .map((item) => ({
      entry: item.webkitGetAsEntry?.() as FileEntry | null | undefined,
      file: item.getAsFile(),
    }));
  const fallback = Array.from(transfer.files);
  return (async () => {
    const result: UploadItem[] = [];
    const add = (file: File, path: string) => {
      signal?.throwIfAborted();
      if (!isAudioFile(path)) return;
      result.push({ file, path });
      if (result.length > 300) throw new Error("每批最多 300 首，请分批上传");
    };
    const visit = async (entry: FileEntry, prefix = ""): Promise<void> => {
      signal?.throwIfAborted();
      if (entry.isFile) {
        if (!isAudioFile(entry.name)) return;
        const file = await new Promise<File>((resolve, reject) =>
          entry.file(resolve, reject),
        );
        add(file, prefix + file.name);
      } else if (entry.isDirectory) {
        const reader = entry.createReader();
        while (true) {
          const page = await new Promise<FileEntry[]>((resolve, reject) =>
            reader.readEntries(resolve, reject),
          );
          signal?.throwIfAborted();
          if (!page.length) break;
          for (const child of page)
            await visit(child, prefix + entry.name + "/");
        }
      }
    };
    if (items.length) {
      for (const item of items) {
        signal?.throwIfAborted();
        if (item.entry) await visit(item.entry);
        else if (item.file)
          add(item.file, item.file.webkitRelativePath || item.file.name);
      }
    } else {
      for (const file of fallback)
        add(file, file.webkitRelativePath || file.name);
    }
    return result;
  })();
}

export function isTextEditing(target: EventTarget | null) {
  return (
    target instanceof Element &&
    Boolean(
      target.closest(
        'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]',
      ),
    )
  );
}
