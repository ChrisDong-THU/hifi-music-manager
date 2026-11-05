// Polls share the current request; a refresh after a mutation must observe a
// request that starts after that mutation, even when an older poll is pending.
export function createRefreshQueue(run: () => Promise<void>) {
  let current: Promise<void> | null = null;
  let next: Promise<void> | null = null;
  function poll(): Promise<void> {
    if (current) return current;
    const pending = Promise.resolve().then(run);
    current = pending.finally(() => {
      current = null;
    });
    return current;
  }
  function refresh(): Promise<void> {
    if (!current) return poll();
    if (next) return next;
    const follow = () => {
      next = null;
      return poll();
    };
    next = current.then(follow, follow);
    return next;
  }
  return { poll, refresh };
}
