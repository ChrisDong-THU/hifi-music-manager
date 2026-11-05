export type RequestOptions = {
  signal?: AbortSignal;
  etag?: string;
  librarySession?: string;
  ifMatch?: string;
};
export type ApiRequest = <T>(
  path: string,
  body?: unknown,
  options?: RequestOptions,
) => Promise<T>;
export type QueryResult<T> =
  | { notModified: true }
  | { notModified: false; data: T; etag: string };
export type ApiQuery = <T>(
  path: string,
  options?: RequestOptions,
) => Promise<QueryResult<T>>;

export function apiFetch(
  path: string,
  body: unknown,
  token: string,
  options: RequestOptions = {},
) {
  const timeout = AbortSignal.timeout(30_000);
  return fetch("/api/" + path, {
    method: body === undefined ? "GET" : "POST",
    cache: "no-store",
    signal: options.signal
      ? AbortSignal.any([options.signal, timeout])
      : timeout,
    headers: {
      ...(options.librarySession
        ? { "X-Library-Session": options.librarySession }
        : {}),
      ...(options.etag ? { "If-None-Match": options.etag } : {}),
      ...(options.ifMatch ? { "If-Match": options.ifMatch } : {}),
      ...(body === undefined
        ? {}
        : {
            ...(body instanceof FormData
              ? {}
              : { "Content-Type": "application/json" }),
            "X-CSRF-Token": token,
          }),
    },
    body:
      body === undefined
        ? undefined
        : body instanceof FormData
          ? body
          : JSON.stringify(body),
  });
}

// A callback belongs to the view that created it. Reading the latest session
// only at send time would silently redirect an old view's action to a new disk.
export function createLibraryClient(
  session: string,
  currentSession: () => string,
  currentToken: () => string,
) {
  function check(path: string, options: RequestOptions) {
    const global = path.split("?")[0] === "system/disks";
    options.signal?.throwIfAborted();
    if (!global && (!session || session !== currentSession()))
      throw new DOMException("曲库已切换，请重试", "AbortError");
    return { ...options, librarySession: global ? undefined : session };
  }
  const request: ApiRequest = async <T>(
    path: string,
    body?: unknown,
    options: RequestOptions = {},
  ): Promise<T> => {
    const response = await apiFetch(
      path,
      body,
      currentToken(),
      check(path, options),
    );
    const result = await jsonResponse<T>(response);
    check(path, options);
    return result;
  };
  const query: ApiQuery = async <T>(
    path: string,
    options: RequestOptions = {},
  ): Promise<QueryResult<T>> => {
    const response = await apiFetch(
      path,
      undefined,
      currentToken(),
      check(path, options),
    );
    const result: QueryResult<T> =
      response.status === 304
        ? { notModified: true }
        : {
            notModified: false,
            data: await jsonResponse<T>(response),
            etag: response.headers.get("ETag") || "",
          };
    check(path, options);
    return result;
  };
  return { request, query };
}

export function libraryMediaURL(path: string, session: string) {
  return `${path}${path.includes("?") ? "&" : "?"}library_session=${encodeURIComponent(session)}`;
}

export async function jsonResponse<T>(response: Response): Promise<T> {
  const data = await response.json().catch(() => {
    throw new Error("服务响应格式无效，请重试");
  });
  if (!response.ok)
    throw new Error(typeof data?.error === "string" ? data.error : "请求失败");
  return data as T;
}
