/** Timeout measures inactivity, so a large file can keep uploading steadily. */
export function uploadFile(
  path: string,
  file: File,
  token: string,
  progress: (fraction: number) => void,
  signal?: AbortSignal,
  librarySession?: string,
): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason || new DOMException("上传已取消", "AbortError"));
      return;
    }
    const xhr = new XMLHttpRequest();
    let timer: ReturnType<typeof setTimeout>;
    let settled = false;
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      xhr.onload = xhr.onerror = xhr.onabort = null;
      xhr.upload.onprogress = xhr.upload.onload = null;
      signal?.removeEventListener("abort", abort);
      if (error) reject(error);
      else resolve();
    };
    const abort = () => {
      finish(new DOMException("上传已取消", "AbortError"));
      xhr.abort();
    };
    signal?.addEventListener("abort", abort, { once: true });
    const watch = (delay: number) => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        finish(new Error("上传响应超时，可重试"));
        xhr.abort();
      }, delay);
    };
    xhr.open("POST", `/api/${path}`);
    xhr.setRequestHeader("X-CSRF-Token", token);
    if (librarySession)
      xhr.setRequestHeader("X-Library-Session", librarySession);
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (event) => {
      watch(120_000);
      if (event.lengthComputable && event.total > 0)
        progress(event.loaded / event.total);
    };
    // Give file inspection/hash validation a separate response budget.
    xhr.upload.onload = () => watch(300_000);
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) return finish();
      let message = "上传失败，可重试";
      try {
        const data: unknown = JSON.parse(xhr.responseText);
        if (
          data &&
          typeof data === "object" &&
          "error" in data &&
          typeof data.error === "string" &&
          data.error.trim()
        )
          message = data.error;
      } catch {
        /* A proxy can return a non-JSON error page. */
      }
      finish(new Error(message));
    };
    xhr.onerror = () => finish(new Error("连接中断，可重试"));
    xhr.onabort = () => finish(new Error("上传已中断，可重试"));
    watch(120_000);
    try {
      xhr.send(file);
    } catch (error) {
      finish(error instanceof Error ? error : new Error("上传失败，可重试"));
    }
  });
}
