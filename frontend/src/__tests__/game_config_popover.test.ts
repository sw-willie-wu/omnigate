import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils';
import { i18n } from '../i18n';

// Auto-unmount each mounted popover after its test so the window-level keydown
// listener (added on open) is removed via onUnmounted — no cross-test leak.
enableAutoUnmount(afterEach);

import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import type { GameRow } from '../stores/games';

const BrowseForDirectory = vi.fn();
const SetGameOverride = vi.fn();
const ClearGameOverride = vi.fn();
const RefreshGame = vi.fn();
const RefreshVersion = vi.fn();
const GetIcon = vi.fn();
const GetBackgrounds = vi.fn();
const ListGames = vi.fn();

vi.mock('../../wailsjs/go/app/App', () => ({
  BrowseForDirectory: (...a: any[]) => BrowseForDirectory(...a),
  SetGameOverride: (...a: any[]) => SetGameOverride(...a),
  ClearGameOverride: (...a: any[]) => ClearGameOverride(...a),
  RefreshGame: (...a: any[]) => RefreshGame(...a),
  RefreshVersion: (...a: any[]) => RefreshVersion(...a),
  GetIcon: (...a: any[]) => GetIcon(...a),
  GetBackgrounds: (...a: any[]) => GetBackgrounds(...a),
  ListGames: (...a: any[]) => ListGames(...a),
}));

import GameConfigPopover from '../components/GameConfigPopover.vue';

function makeRow(over: Partial<GameRow> = {}): GameRow {
  return {
    id: 'hoyoverse/genshin',
    backend: 'hoyoverse',
    display_name: { en: 'Genshin' },
    installed: true,
    has_predownload: false,
    resolved_path: 'C:/Games/Genshin',
    path_source: 'default',
    override_path: '',
    ...over,
  };
}

function mountWith(row: GameRow) {
  return mount(GameConfigPopover, {
    props: { row },
    global: { plugins: [i18n] },
  });
}

describe('GameConfigPopover', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    i18n.global.locale.value = 'en';
    BrowseForDirectory.mockReset().mockResolvedValue('');
    SetGameOverride.mockReset().mockImplementation((id: string) => Promise.resolve(makeRow({ id })));
    ClearGameOverride.mockReset().mockImplementation((id: string) => Promise.resolve(makeRow({ id })));
    RefreshGame.mockReset().mockResolvedValue(makeRow());
    RefreshVersion.mockReset().mockResolvedValue({ Current: '1', Latest: '1', Predownload: false });
    GetIcon.mockReset().mockResolvedValue('');
    GetBackgrounds.mockReset().mockResolvedValue([]);
  });

  it('renders the gear button even when the game is not installed', () => {
    const w = mountWith(makeRow({ installed: false, path_source: 'unresolved' }));
    expect(w.find('[data-testid="game-config-btn"]').exists()).toBe(true);
  });

  it('clicking the gear shows the popover', async () => {
    const w = mountWith(makeRow());
    expect(w.find('[data-testid="game-config-popover"]').exists()).toBe(false);
    await w.find('[data-testid="game-config-btn"]').trigger('click');
    expect(w.find('[data-testid="game-config-popover"]').exists()).toBe(true);
  });

  it('source badge text matches path_source (default)', async () => {
    const w = mountWith(makeRow({ path_source: 'default' }));
    await w.find('[data-testid="game-config-btn"]').trigger('click');
    expect(w.find('[data-testid="game-config-source"]').text()).toBe('Default location');
  });

  it('source badge shows invalid when override set but not installed', async () => {
    const w = mountWith(makeRow({ path_source: 'override', installed: false, override_path: 'C:/Gone' }));
    await w.find('[data-testid="game-config-btn"]').trigger('click');
    expect(w.find('[data-testid="game-config-source"]').text()).toBe('Set, but not found');
  });

  it('Save calls games.setOverride / SetGameOverride', async () => {
    const w = mountWith(makeRow());
    const games = useGamesStore();
    const spy = vi.spyOn(games, 'setOverride');
    await w.find('[data-testid="game-config-btn"]').trigger('click');
    const input = w.find('[data-testid="game-config-popover"] input[type="text"]');
    await input.setValue('D:/NewPath');
    await w.find('[data-testid="game-config-save"]').trigger('click');
    await flushPromises();
    expect(spy).toHaveBeenCalledWith('hoyoverse/genshin', 'D:/NewPath');
  });

  it('Save is disabled while an update is in-flight for this game', async () => {
    const row = makeRow();
    const w = mountWith(row);
    const updates = useUpdatesStore();
    updates.byGame[row.id] = { in_flight: { phase: 'download' } } as any;
    await w.find('[data-testid="game-config-btn"]').trigger('click');
    // seed a draft so emptiness isn't the cause of disabled
    const input = w.find('[data-testid="game-config-popover"] input[type="text"]');
    await input.setValue('D:/x');
    expect(w.find('[data-testid="game-config-save"]').attributes('disabled')).toBeDefined();
  });

  it('ESC closes the popover', async () => {
    const w = mountWith(makeRow());
    await w.find('[data-testid="game-config-btn"]').trigger('click');
    expect(w.find('[data-testid="game-config-popover"]').exists()).toBe(true);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(w.find('[data-testid="game-config-popover"]').exists()).toBe(false);
  });
});
