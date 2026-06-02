import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

vi.mock('../../wailsjs/go/app/App', () => ({
  SetGameOverride: vi.fn(async (id: string, path: string) => ({ id, backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false, resolved_path: path, path_source: 'override', override_path: path })),
  ClearGameOverride: vi.fn(async (id: string) => ({ id, backend: 'kurogames', display_name: { en: 'W' }, installed: true, has_predownload: false, resolved_path: 'C:/def', path_source: 'default', override_path: '' })),
  RefreshGame: vi.fn(),
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(async () => ''), GetBackgrounds: vi.fn(async () => []),
}));

import { useGamesStore } from '../stores/games';

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
