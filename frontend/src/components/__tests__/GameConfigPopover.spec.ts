import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const getBundleState = vi.fn();
const setActive = vi.fn();
const install = vi.fn();
const remove = vi.fn();
const setOption = vi.fn();
const isRunning = vi.fn();
const confirmFn = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetBundleState: (...a: unknown[]) => getBundleState(...a),
  SetActiveBundle: (...a: unknown[]) => setActive(...a),
  InstallBundle: (...a: unknown[]) => install(...a),
  RemoveBundle: (...a: unknown[]) => remove(...a),
  SetLaunchOption: (...a: unknown[]) => setOption(...a),
  DiscardInterrupted: vi.fn(),
  IsGameRunning: (...a: unknown[]) => isRunning(...a),
  BrowseForDirectory: vi.fn(),
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(), GetBackgrounds: vi.fn(),
  SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(), RefreshGame: vi.fn(), GetCustomBackground: vi.fn(),
  StartUpdate: vi.fn(), CancelInFlight: vi.fn(), StartPredownload: vi.fn(), ApplyPredownload: vi.fn(),
  RemovePredownload: vi.fn(), DismissError: vi.fn(), ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(),
  CheckForUpdate: vi.fn(), RelaunchElevated: vi.fn(),
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));
vi.mock('../../composables/useToast', () => ({ pushToast: vi.fn(), registerToast: vi.fn() }));
vi.mock('../../composables/useDialog', () => ({ confirm: (...a: unknown[]) => confirmFn(...a), registerDialog: vi.fn() }));

import GameConfigPopover from '../GameConfigPopover.vue';
import { useUpdatesStore } from '../../stores/updates';

const GID = 'kurogames/wutheringwaves';
const row = { id: GID, backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false, path_source: 'launcher', resolved_path: 'C:/g', override_path: '' };
const state = (over = {}) => ({
  supported: true, active: 'HD', catalog_stale: false,
  bundles: [
    { name: 'UHD', display_name: { en: 'UHD' }, installed: false, pending: false, size_bytes: 66e9, launch_supported: true, removable: false },
    { name: 'HD', display_name: { en: 'HD' }, installed: true, pending: false, version: '3.7.0', size_bytes: 45e9, launch_supported: true, removable: false },
    { name: 'SD', display_name: { en: 'SD' }, installed: true, pending: false, version: '3.7.0', size_bytes: 22e9, launch_supported: true, removable: true },
  ],
  options: [{ cmd: '-slno', label: { en: 'Disable DLSS' }, enabled: false, default: false }],
  ...over,
});

async function open(st = state(), running = false) {
  setActivePinia(createPinia());
  getBundleState.mockResolvedValue(st);
  isRunning.mockResolvedValue(running);
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const w = mount(GameConfigPopover, { props: { row: row as never }, global: { plugins: [i18n] } });
  await w.find('[data-testid="game-config-btn"]').trigger('click');
  await flushPromises();
  return w;
}

describe('GameConfigPopover bundles', () => {
  beforeEach(() => { [getBundleState, setActive, install, remove, setOption, isRunning, confirmFn].forEach((m) => m.mockReset()); });

  it('shows remove only on installed non-active and install on not-installed', async () => {
    const w = await open();
    expect(w.find('[data-testid="bundle-remove-SD"]').exists()).toBe(true);
    expect(w.find('[data-testid="bundle-remove-HD"]').exists()).toBe(false);
    expect(w.find('[data-testid="bundle-install-UHD"]').exists()).toBe(true);
  });

  it('install asks for confirmation and does not call RPC on cancel', async () => {
    confirmFn.mockResolvedValue('cancel');
    const w = await open();
    await w.find('[data-testid="bundle-install-UHD"]').trigger('click');
    await flushPromises();
    expect(confirmFn).toHaveBeenCalled();
    expect(install).not.toHaveBeenCalled();
  });

  it('install calls RPC after confirm', async () => {
    confirmFn.mockResolvedValue('ok');
    install.mockResolvedValue(state());
    const w = await open();
    await w.find('[data-testid="bundle-install-UHD"]').trigger('click');
    await flushPromises();
    expect(install).toHaveBeenCalledWith(GID, 'UHD');
  });

  it('pending row is disabled', async () => {
    const st = state();
    st.bundles[0] = { ...st.bundles[0], pending: true };
    const w = await open(st);
    expect(w.find('[data-testid="bundle-install-UHD"]').exists()).toBe(false);
    expect(w.find('[data-testid="bundle-row-UHD"]').classes()).toContain('disabled');
  });

  it('in-flight disables both sections', async () => {
    const w = await open();
    const updates = useUpdatesStore();
    updates.byGame[GID] = { in_flight: { kind: 'update', phase: 'download', current: 0, total: 1, version: '3.7.0', started_at: '' } } as never;
    await flushPromises();
    expect(w.find('[data-testid="bundle-radio-SD"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="option--slno"]').attributes('disabled')).toBeDefined();
  });

  it('toggling an option calls SetLaunchOption', async () => {
    setOption.mockResolvedValue(state());
    const w = await open();
    await w.find('[data-testid="option--slno"]').setValue(true);
    await flushPromises();
    expect(setOption).toHaveBeenCalledWith(GID, '-slno', true);
  });

  it('selecting an installed bundle calls SetActiveBundle', async () => {
    setActive.mockResolvedValue(state({ active: 'SD' }));
    const w = await open();
    await w.find('[data-testid="bundle-radio-SD"]').setValue(true);
    await flushPromises();
    expect(setActive).toHaveBeenCalledWith(GID, 'SD');
  });
});
