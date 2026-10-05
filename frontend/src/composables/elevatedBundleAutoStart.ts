// runElevatedBundleAutoStart: when the elevated relaunch passed
// --elevate-install-bundle <gid> <bundle> (surfaced by the backend's
// PendingElevatedBundle RPC), select that game and continue the bundle install.
// No confirm dialog: the user already confirmed before the relaunch (spec §6.9).
// Never rejects — a failure here must not break app startup.
export async function runElevatedBundleAutoStart(
  pendingFn: () => Promise<{ game_id: string; bundle: string }>,
  select: (id: string) => void,
  install: (id: string, bundle: string) => Promise<unknown>,
): Promise<void> {
  try {
    const p = await pendingFn();
    if (p?.game_id && p?.bundle) {
      select(p.game_id);
      await install(p.game_id, p.bundle);
    }
  } catch (e) {
    console.warn('elevated bundle auto-start failed', e);
  }
}
