import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils';
import { i18n } from '../i18n';

// Auto-unmount each mounted panel after its test so the window-level keydown
// listener (added on open) is removed via onUnmounted — no cross-test leak.
enableAutoUnmount(afterEach);
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';
import { useGamesStore } from '../stores/games';

const sampleSettings = () => ({
  Version: 1,
  App: { Language: 'zh-TW', TempDir: '', BannerAnimationPref: 'video-when-available', ShowTechnicalInfo: false },
  Backends: {
    Hoyoverse: { Path: 'C:/HoYoPlay', Region: 'global', TempDir: '' },
    Kurogames: { Path: 'C:/Wuthering', TempDir: '' },
    Hypergryph: { Path: 'C:/Endfield', TempDir: '' },
  },
  // Per-game overrides set via the BottomBar popover live here; the settings
  // panel must round-trip them untouched on Save.
  Games: { 'kurogames/wutheringwaves': { Path: 'D:/WW' } },
});

const GetSettings = vi.fn();
const UpdateSettings = vi.fn();
const BrowseForDirectory = vi.fn();
const BrowseForImage = vi.fn(() => Promise.resolve(''));
vi.mock('../../wailsjs/go/app/App', () => ({
  GetSettings: (...a: any[]) => GetSettings(...a),
  UpdateSettings: (...a: any[]) => UpdateSettings(...a),
  BrowseForDirectory: (...a: any[]) => BrowseForDirectory(...a),
  BrowseForImage: (...a: any[]) => BrowseForImage(...a),
  Refresh: vi.fn(() => Promise.resolve()),
}));
// refreshAll pulls stores; stub the games store loaders it calls.
vi.mock('../composables/useRefreshAll', () => ({ refreshAll: vi.fn(() => Promise.resolve()) }));

import SettingsPanel from '../components/SettingsPanel.vue';

function mountOpen() {
  const wrapper = mount(SettingsPanel, { global: { plugins: [i18n], stubs: { teleport: true } } });
  const view = useViewStore();
  view.openSettings();
  return wrapper;
}

describe('SettingsPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    i18n.global.locale.value = 'zh-TW';
    GetSettings.mockReset().mockResolvedValue(sampleSettings());
    UpdateSettings.mockReset().mockResolvedValue(undefined);
    BrowseForDirectory.mockReset().mockResolvedValue('');
  });

  it('loads settings into the draft on open', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(GetSettings).toHaveBeenCalled();
    expect(w.find('input[data-test="settings-tempdir"]').exists()).toBe(true);
    expect((w.find('input[data-test="settings-tempdir"]').element as HTMLInputElement).value).toBe('');
  });

  it('Save calls UpdateSettings with the (edited) draft, preserving unshown fields', async () => {
    const w = mountOpen();
    await flushPromises();
    await w.find('input[data-test="settings-tempdir"]').setValue('D:/NewTemp');
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).toHaveBeenCalledTimes(1);
    const arg = UpdateSettings.mock.calls[0][0];
    expect(arg.App.TempDir).toBe('D:/NewTemp');
    expect(arg.Backends.Hoyoverse.Path).toBe('C:/HoYoPlay');
    expect(arg.App.Language).toBe('zh-TW');
    expect(arg.Games['kurogames/wutheringwaves'].Path).toBe('D:/WW');
  });

  it('Cancel closes without calling UpdateSettings', async () => {
    const w = mountOpen();
    await flushPromises();
    await w.find('[data-test="settings-cancel"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).not.toHaveBeenCalled();
    expect(useViewStore().settingsOpen).toBe(false);
  });

  it('ESC closes the drawer (window-level listener)', async () => {
    const w = mountOpen();
    await flushPromises();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(useViewStore().settingsOpen).toBe(false);
    expect(UpdateSettings).not.toHaveBeenCalled();
  });

  it('Save is disabled while an update is in-flight', async () => {
    const w = mountOpen();
    await flushPromises();
    const updates = useUpdatesStore();
    updates.byGame['hoyoverse/genshin'] = { in_flight: { phase: 'download' } } as any;
    await flushPromises();
    expect(w.find('[data-test="settings-save"]').attributes('disabled')).toBeDefined();
  });

  it('Save error keeps the panel open and shows the error', async () => {
    UpdateSettings.mockRejectedValueOnce(new Error('disk full'));
    const w = mountOpen();
    await flushPromises();
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(useViewStore().settingsOpen).toBe(true);
    expect(w.html()).toContain('disk full');
  });

  it('renders a language dropdown with three options', async () => {
    const w = mountOpen();
    await flushPromises();
    const select = w.find('select[data-test="settings-language"]');
    expect(select.exists()).toBe(true);
    expect(select.findAll('option')).toHaveLength(3);
  });

  it('applies language live on change and reverts on cancel', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(i18n.global.locale.value).toBe('zh-TW');
    await w.find('select[data-test="settings-language"]').setValue('en');
    expect(i18n.global.locale.value).toBe('en');
    await w.find('[data-test="settings-cancel"]').trigger('click');
    await flushPromises();
    expect(i18n.global.locale.value).toBe('zh-TW');
  });

  it('lists games with a custom background field bound to Games[id].BackgroundPath', async () => {
    const games = useGamesStore();
    games.games = [{ id: 'hoyoverse/genshin', backend: 'hoyoverse', display_name: { 'zh-TW': '原神', en: 'Genshin' }, installed: true, has_predownload: false } as any];
    const w = mountOpen();
    await flushPromises();
    expect(w.find('input[data-test="settings-custombg-hoyoverse/genshin"]').exists()).toBe(true);
  });

  it('browse sets the custom bg path and clear empties it (preserving existing Path)', async () => {
    const games = useGamesStore();
    games.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false } as any];
    BrowseForImage.mockResolvedValueOnce('D:/custom.png');
    const w = mountOpen();
    await flushPromises();
    const input = w.find('input[data-test="settings-custombg-kurogames/wutheringwaves"]');
    await w.findAll('.settings-group')[1].find('.settings-browse').trigger('click');
    await flushPromises();
    expect((input.element as HTMLInputElement).value).toBe('D:/custom.png');
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    const arg = UpdateSettings.mock.calls[0][0];
    expect(arg.Games['kurogames/wutheringwaves'].BackgroundPath).toBe('D:/custom.png');
    expect(arg.Games['kurogames/wutheringwaves'].Path).toBe('D:/WW'); // existing Path preserved
  });
});
