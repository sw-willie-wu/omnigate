import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useBackendsStore } from '../stores/backends';
import { Refresh } from '../../wailsjs/go/app/App';

// refreshAll re-detects installs, reloads game data + assets, and probes each
// installed game for updates. Shared by the Topbar refresh button and the
// settings panel's post-Save refresh so they cannot drift.
export async function refreshAll(): Promise<void> {
  await Refresh();
  const games = useGamesStore();
  const updates = useUpdatesStore();
  const backends = useBackendsStore();
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
  await backends.load();
  await Promise.allSettled(
    games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
  );
}
