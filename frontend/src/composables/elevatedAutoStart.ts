// runElevatedAutoStart: when the elevated relaunch passed --elevate-update <gid>
// (surfaced by the backend's PendingElevatedGame RPC), select that game and start
// its update. Store-agnostic (callbacks injected) so it is unit-testable without
// mounting App.vue. Never rejects — a failure here must not break app startup.
export async function runElevatedAutoStart(
  pendingFn: () => Promise<string>,
  select: (id: string) => void,
  startUpdate: (id: string) => Promise<void>,
): Promise<void> {
  try {
    const pending = await pendingFn();
    if (pending) {
      select(pending);
      await startUpdate(pending);
    }
  } catch (e) {
    console.warn('elevated auto-start failed', e);
  }
}
