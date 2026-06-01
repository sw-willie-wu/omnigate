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
    Hypergryph: { Path: 'C:/Endfield' },
  },
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
    const firstInput = w.findAll('input[type="text"]')[0];
    expect((firstInput.element as HTMLInputElement).value).toBe('C:/HoYoPlay');
  });

  it('Save calls UpdateSettings with the (edited) draft, preserving unshown fields', async () => {
    const w = mountOpen();
    await flushPromises();
    // Edit the hoyoverse path input (first text input in the panel).
    const input = w.findAll('input[type="text"]')[0];
    await input.setValue('D:/NewHoYo');
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).toHaveBeenCalledTimes(1);
    const arg = UpdateSettings.mock.calls[0][0];
    expect(arg.Backends.Hoyoverse.Path).toBe('D:/NewHoYo');
    expect(arg.App.Language).toBe('zh-TW'); // unshown field preserved
    expect(arg.Backends.Hoyoverse.Region).toBe('global'); // preserved
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
