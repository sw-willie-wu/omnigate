import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

vi.mock('../../wailsjs/go/app/App', () => ({
  SetGameOverride: vi.fn(async (id: string, path: string) => ({ id, backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false, resolved_path: path, path_source: 'override', override_path: path })),
  ClearGameOverride: vi.fn(async (id: string) => ({ id, backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false, resolved_path: 'C:/def', path_source: 'default', override_path: '' })),
  RefreshGame: vi.fn(),
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(async () => ''), GetBackgrounds: vi.fn(async () => []),
  GetCustomBackground: vi.fn(async () => ''),
}));

import { useGamesStore } from '../stores/games';
import * as AppMod from '../../wailsjs/go/app/App';

describe('games store per-game override', () => {
  beforeEach(() => setActivePinia(createPinia()));
  it('setOverride replaces the row with the returned GameRow', async () => {
    const s = useGamesStore();
    s.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'W' }, installed: false, has_predownload: false }] as any;
    await s.setOverride('kurogames/wutheringwaves', 'D:/WW');
    const row = s.games.find((g) => g.id === 'kurogames/wutheringwaves')!;
    expect(row.path_source).toBe('override');
    expect(row.override_path).toBe('D:/WW');
    expect(row.installed).toBe(true);
  });
  it('clearOverride reverts the row to detection source', async () => {
    const s = useGamesStore();
    s.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false, path_source: 'override', override_path: 'D:/WW' }] as any;
    await s.clearOverride('kurogames/wutheringwaves');
    const row = s.games.find((g) => g.id === 'kurogames/wutheringwaves')!;
    expect(row.path_source).toBe('default');
    expect(row.override_path).toBe('');
  });
});

describe('games store backgrounds[] (no collapse)', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('fills backgrounds[] (no collapse) and seeds bgIndex in range', async () => {
    vi.mocked(AppMod.GetBackgrounds).mockResolvedValue([
      { ImageURL: 'img1', VideoURL: 'vid1', Type: 'bg' },
      { ImageURL: 'img2', VideoURL: '',     Type: 'bg' },
      { ImageURL: 'img3', VideoURL: 'vid3', Type: 'bg' },
    ] as any);
    vi.mocked(AppMod.GetCustomBackground).mockResolvedValue('');

    const store = useGamesStore();
    store.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false }] as any;
    await store.loadAssetsFor('kurogames/wutheringwaves');

    const g = store.games.find((x) => x.id === 'kurogames/wutheringwaves')!;
    expect(g.backgrounds!.length).toBe(3);
    expect(g.bgIndex!).toBeGreaterThanOrEqual(0);
    expect(g.bgIndex!).toBeLessThan(3);
  });

  it('custom background replaces official list', async () => {
    vi.mocked(AppMod.GetCustomBackground).mockResolvedValue('data:image/png;base64,AAA');

    const store = useGamesStore();
    store.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false }] as any;
    await store.loadAssetsFor('kurogames/wutheringwaves');

    const g = store.games.find((x) => x.id === 'kurogames/wutheringwaves')!;
    expect(g.backgrounds).toEqual([{ image: 'data:image/png;base64,AAA', video: '' }]);
  });
});
