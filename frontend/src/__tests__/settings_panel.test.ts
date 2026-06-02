import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils';
import { i18n } from '../i18n';

// Auto-unmount each mounted panel after its test so the window-level keydown
// listener (added on open) is removed via onUnmounted — no cross-test leak.
enableAutoUnmount(afterEach);
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';

const sampleSettings = () => ({
  Version: 1,
  App: { Language: 'zh-TW', BannerAnimationPref: 'video-when-available', ShowTechnicalInfo: false },
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
vi.mock('../../wailsjs/go/app/App', () => ({
  GetSettings: (...a: any[]) => GetSettings(...a),
  UpdateSettings: (...a: any[]) => UpdateSettings(...a),
  BrowseForDirectory: (...a: any[]) => BrowseForDirectory(...a),
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
    GetSettings.mockReset().mockResolvedValue(sampleSettings());
    UpdateSettings.mockReset().mockResolvedValue(undefined);
    BrowseForDirectory.mockReset().mockResolvedValue('');
  });

  it('loads settings into the draft on open', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(GetSettings).toHaveBeenCalled();
    // First text input should now be HoYoverse TempDir (empty string)
    const firstInput = w.findAll('input[type="text"]')[0];
    expect((firstInput.element as HTMLInputElement).value).toBe('');
    // Verify no Path inputs exist (they were removed)
    const pathInputs = w.findAll('input[type="text"]').filter(
      (input) => (input.element as HTMLInputElement).value === 'C:/HoYoPlay'
    );
    expect(pathInputs).toHaveLength(0);
  });

  it('Save calls UpdateSettings with the (edited) draft, preserving unshown fields', async () => {
    const w = mountOpen();
    await flushPromises();
    // Edit the HoYoverse TempDir input (first text input in the panel, now that Path is removed).
    const input = w.findAll('input[type="text"]')[0];
    await input.setValue('D:/NewTemp');
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).toHaveBeenCalledTimes(1);
    const arg = UpdateSettings.mock.calls[0][0];
    // Verify TempDir was edited
    expect(arg.Backends.Hoyoverse.TempDir).toBe('D:/NewTemp');
    // Verify Path is PRESERVED (unchanged, though not shown in the UI)
    expect(arg.Backends.Hoyoverse.Path).toBe('C:/HoYoPlay');
    expect(arg.App.Language).toBe('zh-TW'); // unshown field preserved
    expect(arg.Backends.Hoyoverse.Region).toBe('global'); // preserved
    // Per-game overrides (set via the popover) must survive a settings Save.
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
});
