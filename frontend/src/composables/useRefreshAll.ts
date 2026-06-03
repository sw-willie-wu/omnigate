import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useNewsStore } from '../stores/news';
import { Refresh } from '../../wailsjs/go/app/App';

// refreshAll re-detects installs, reloads game data + assets, probes each
// installed game for updates, and clears the news cache so the panel refetches.
// Shared by the Topbar refresh button and the settings panel's post-Save
// refresh so they cannot drift.
export async function refreshAll(): Promise<void> {
  await Refresh();
  const games = useGamesStore();
  const updates = useUpdatesStore();
  const news = useNewsStore();
  news.reset(); // bypass the lazy `loaded` guard so news refetches on next view
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
  await Promise.allSettled(
    games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
  );
}
