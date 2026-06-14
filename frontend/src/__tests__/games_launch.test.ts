import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const LaunchMock = vi.fn();
vi.mock('../../wailsjs/go/app/App', () => ({
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(),
  GetBackgrounds: vi.fn(), SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(),
  RefreshGame: vi.fn(), GetCustomBackground: vi.fn(),
  Launch: (...a: unknown[]) => LaunchMock(...a),
}));

import { useGamesStore } from '../stores/games';

describe('games.launchGame', () => {
  beforeEach(() => { setActivePinia(createPinia()); LaunchMock.mockReset(); });

  it('optimistically sets last_played by direct field mutation (preserves assets)', async () => {
    LaunchMock.mockResolvedValue(0);
    const games = useGamesStore();
    games.games = [{
      id: 'fake/g', backend: 'fake', display_name: { en: 'G' },
      installed: true, has_predownload: false,
      icon_url: 'icon://x',
    }];
    games.selectedID = 'fake/g';

    await games.launchGame('fake/g');

    expect(LaunchMock).toHaveBeenCalledWith('fake/g', '');
    const row = games.games[0];
    expect(row.last_played).toBeTruthy();
    expect(row.icon_url).toBe('icon://x');
  });

  it('does not stamp last_played when Launch rejects', async () => {
    LaunchMock.mockRejectedValue(new Error('nope'));
    const games = useGamesStore();
    games.games = [{ id: 'fake/g', backend: 'fake', display_name: {}, installed: true, has_predownload: false }];
    await expect(games.launchGame('fake/g')).rejects.toThrow();
    expect(games.games[0].last_played).toBeUndefined();
  });
});
