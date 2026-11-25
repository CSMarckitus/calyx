const flag = "calyx-docs-reloaded";

// A chunk of an old build is gone after a new release. One reload gets the new build. The flag
// stops a second reload, so a chunk that is really missing cannot start a loop.
export function reloadOnce(storage: Storage, reload: () => void): void {
  try {
    if (storage.getItem(flag) !== null) return;
    storage.setItem(flag, "1");
  } catch {
    // Storage is blocked: no flag, so no reload.
    return;
  }
  reload();
}
