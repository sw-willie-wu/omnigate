import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils';
import { i18n } from '../i18n';

// Auto-unmount each mounted panel after its test so the window-level keydown
// listener (added on open) is removed via onUnmounted — no cross-test leak.
enableAutoUnmount(afterEach);
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';

const sampleSettings = () => ({
  Version: 3,
  App: { Language: 'zh-TW', TempDir: '', BannerAnimationPref: 'video-when-available', ShowTechnicalInfo: false },
  Backends: {
    Hoyoverse: { Path: 'C:/HoYoPlay', Region: 'global' },
    Kurogames: { Path: 'C:/Wuthering' },
    Hypergryph: { Path: 'C:/Endfield' },
  },
  // Per-game overrides (e.g. install path set via the BottomBar popover) live
  // here; the live settings panel must preserve them when writing other fields.
  Games: { 'kurogames/wutheringwaves': { Path: 'D:/WW' } },
});

const GetSettings = vi.fn();
const UpdateSettings = vi.fn();
const SetLanguage = vi.fn(() => Promise.resolve());
const BrowseForDirectory = vi.fn();
const BrowseForImage = vi.fn(() => Promise.resolve(''));
const GetIcon = vi.fn(() => Promise.resolve('icon://x'));
const GetBackgrounds = vi.fn(() => Promise.resolve([]));
const GetCustomBackground = vi.fn(() => Promise.resolve(''));
vi.mock('../../wailsjs/go/app/App', () => ({
  GetSettings: (...a: any[]) => GetSettings(...a),
  UpdateSettings: (...a: any[]) => UpdateSettings(...a),
  SetLanguage: (...a: any[]) => SetLanguage(...a),
  BrowseForDirectory: (...a: any[]) => BrowseForDirectory(...a),
  BrowseForImage: (...a: any[]) => BrowseForImage(...a),
  GetIcon: (...a: any[]) => GetIcon(...a),
  GetBackgrounds: (...a: any[]) => GetBackgrounds(...a),
  GetCustomBackground: (...a: any[]) => GetCustomBackground(...a),
}));

import SettingsPanel from '../components/SettingsPanel.vue';

function mountOpen() {
  const wrapper = mount(SettingsPanel, { global: { plugins: [i18n], stubs: { teleport: true } } });
  const view = useViewStore();
  view.openSettings();
  return wrapper;
}

function lastUpdateArg() {
  return UpdateSettings.mock.calls[UpdateSettings.mock.calls.length - 1][0];
}

describe('SettingsPanel (live settings)', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    i18n.global.locale.value = 'zh-TW';
    GetSettings.mockReset().mockResolvedValue(sampleSettings());
    UpdateSettings.mockReset().mockResolvedValue(undefined);
    SetLanguage.mockReset().mockResolvedValue(undefined);
    BrowseForDirectory.mockReset().mockResolvedValue('');
    BrowseForImage.mockReset().mockResolvedValue('');
  });

  it('loads settings on open', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(GetSettings).toHaveBeenCalled();
    expect(w.find('input[data-test="settings-tempdir"]').exists()).toBe(true);
    expect((w.find('input[data-test="settings-tempdir"]').element as HTMLInputElement).value).toBe('');
  });

  it('renders a language dropdown with three options', async () => {
    const w = mountOpen();
    await flushPromises();
    const select = w.find('select[data-test="settings-language"]');
    expect(select.exists()).toBe(true);
    expect(select.findAll('option')).toHaveLength(3);
  });

  it('changing language live-applies and persists immediately (no Save needed)', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(i18n.global.locale.value).toBe('zh-TW');
    await w.find('select[data-test="settings-language"]').setValue('en');
    await flushPromises();
    expect(i18n.global.locale.value).toBe('en');
    expect(SetLanguage).toHaveBeenCalledWith('en');
  });

  it('editing the temp dir persists immediately on change (preserving unshown fields)', async () => {
    const w = mountOpen();
    await flushPromises();
    const input = w.find('input[data-test="settings-tempdir"]');
    await input.setValue('D:/NewTemp');
    await input.trigger('change');
    await flushPromises();
    expect(UpdateSettings).toHaveBeenCalled();
    const arg = lastUpdateArg();
    expect(arg.App.TempDir).toBe('D:/NewTemp');
    expect(arg.Backends.Hoyoverse.Path).toBe('C:/HoYoPlay'); // unshown field preserved
    expect(arg.Games['kurogames/wutheringwaves'].Path).toBe('D:/WW'); // per-game override preserved
  });

  it('ESC closes the panel without persisting', async () => {
    mountOpen();
    await flushPromises();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(useViewStore().settingsOpen).toBe(false);
    expect(UpdateSettings).not.toHaveBeenCalled();
  });

  it('a failed persist surfaces an inline error and keeps the panel open', async () => {
    UpdateSettings.mockRejectedValueOnce(new Error('disk full'));
    const w = mountOpen();
    await flushPromises();
    // Temp-dir "clear" → setTempDir('') → single persist() that rejects.
    await w.find('.settings-clear').trigger('click');
    await flushPromises();
    expect(useViewStore().settingsOpen).toBe(true);
    expect(w.html()).toContain('disk full');
  });

  it('lists games with a custom background field bound to Games[id].BackgroundPath', async () => {
    const games = useGamesStore();
    games.games = [{ id: 'hoyoverse/genshin', backend: 'hoyoverse', display_name: { 'zh-TW': '原神', en: 'Genshin' }, installed: true, has_predownload: false } as any];
    const w = mountOpen();
    await flushPromises();
    expect(w.find('input[data-test="settings-custombg-hoyoverse/genshin"]').exists()).toBe(true);
  });

  it('browsing a custom background persists it immediately (preserving existing Path)', async () => {
    const games = useGamesStore();
    games.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false } as any];
    BrowseForImage.mockResolvedValueOnce('D:/custom.png');
    const w = mountOpen();
    await flushPromises();
    const input = w.find('input[data-test="settings-custombg-kurogames/wutheringwaves"]');
    await w.findAll('.settings-group')[1].find('.settings-browse').trigger('click');
    await flushPromises();
    expect((input.element as HTMLInputElement).value).toBe('D:/custom.png');
    expect(UpdateSettings).toHaveBeenCalled();
    const arg = lastUpdateArg();
    expect(arg.Games['kurogames/wutheringwaves'].BackgroundPath).toBe('D:/custom.png');
    expect(arg.Games['kurogames/wutheringwaves'].Path).toBe('D:/WW'); // existing Path preserved
  });

  it('clearing a custom background persists the empty value immediately', async () => {
    const games = useGamesStore();
    games.games = [{ id: 'kurogames/wutheringwaves', backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false } as any];
    GetSettings.mockResolvedValue({ ...sampleSettings(), Games: { 'kurogames/wutheringwaves': { Path: 'D:/WW', BackgroundPath: 'D:/old.png' } } });
    const w = mountOpen();
    await flushPromises();
    await w.findAll('.settings-group')[1].find('.settings-clear').trigger('click');
    await flushPromises();
    const arg = lastUpdateArg();
    expect(arg.Games['kurogames/wutheringwaves'].BackgroundPath).toBe('');
    expect(arg.Games['kurogames/wutheringwaves'].Path).toBe('D:/WW');
  });
});
