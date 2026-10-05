import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';
import tw from '../../locales/zh-TW.json';
import cn from '../../locales/zh-CN.json';

const isRunning = vi.fn();
const launch = vi.fn();
const isElevated = vi.fn();
const relaunchElevated = vi.fn();
const relaunchForBundle = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  IsGameRunning: (...a: unknown[]) => isRunning(...a),
  IsElevated: (...a: unknown[]) => isElevated(...a),
  RelaunchElevated: (...a: unknown[]) => relaunchElevated(...a),
  RelaunchElevatedForBundle: (...a: unknown[]) => relaunchForBundle(...a),
  GetBundleState: vi.fn(), SetActiveBundle: vi.fn(), InstallBundle: vi.fn(), RemoveBundle: vi.fn(),
  SetLaunchOption: vi.fn(), DiscardInterrupted: vi.fn(), RelaunchAsAdmin: vi.fn(),
  Launch: (...a: unknown[]) => launch(...a),
  // module-load deps of the games/updates stores:
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(), GetBackgrounds: vi.fn(),
  SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(), RefreshGame: vi.fn(), GetCustomBackground: vi.fn(),
  ListGameAccounts: vi.fn(), SetAccountLabel: vi.fn(),
  StartUpdate: vi.fn(), CancelInFlight: vi.fn(), StartPredownload: vi.fn(),
  ApplyPredownload: vi.fn(), RemovePredownload: vi.fn(), CheckForUpdate: vi.fn(),
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));
const pushToast = vi.fn();
vi.mock('../../composables/useToast', () => ({ pushToast: (...a: unknown[]) => pushToast(...a), registerToast: vi.fn() }));
vi.mock('../../composables/useDialog', () => ({ confirm: vi.fn(), registerDialog: vi.fn() }));

import BottomBar from '../BottomBar.vue';
import { useGamesStore } from '../../stores/games';
import { useAccountStore } from '../../stores/account';
import { useUpdatesStore } from '../../stores/updates';
import { setLang } from '../../i18n';

const GID = 'kurogames/wutheringwaves';
function setup(opts: { selectedId: string; running?: boolean; locale?: 'en' | 'zh-TW' }) {
  setActivePinia(createPinia());
  isRunning.mockResolvedValue(!!opts.running);
  const games = useGamesStore();
  games.games = [{ id: GID, backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false, current_version: '1.0' }] as never;
  games.selectedID = GID;
  const account = useAccountStore();
  account.byGid[GID] = {
    accounts: [
      { id: 'A', uid: 'uA', label: '', email: 'a@x', username: 'UA', active: true },
      { id: 'B', uid: 'uB', label: 'Bee', email: 'b@x', username: 'UB', active: false },
    ],
    selectedId: opts.selectedId, loaded: true, loading: false,
  };
  const i18n = createI18n({ legacy: false, locale: opts.locale ?? 'en', messages: { en, 'zh-TW': tw } });
  return mount(BottomBar, { global: { plugins: [i18n], stubs: { GameConfigPopover: true } } });
}

describe('BottomBar Play states', () => {
  beforeEach(() => { isRunning.mockReset(); launch.mockReset(); pushToast.mockReset(); });

  it('shows 開始遊戲 (enabled) when selected == active and not running', async () => {
    const w = setup({ selectedId: 'A' }); await flushPromises();
    const btn = w.find('.launch-area .launch-btn');
    expect(btn.text()).toContain(en.buttons.play);
    expect(btn.attributes('disabled')).toBeUndefined();
  });

  it('shows 以 {name} 啟動 when the selected account differs from the active one', async () => {
    const w = setup({ selectedId: 'B' }); await flushPromises();
    expect(w.find('.launch-area .launch-btn').text()).toContain('Bee');
  });

  it('shows the running label (disabled) when the game is running', async () => {
    const w = setup({ selectedId: 'A', running: true }); await flushPromises();
    const btn = w.find('.launch-area .launch-btn');
    expect(btn.text()).toContain(en.buttons.launching);
    expect(btn.attributes('disabled')).toBeDefined();
  });

  it('toasts the game-running message when launch fails with ErrGameRunning', async () => {
    launch.mockRejectedValue(new Error('game is running'));
    const w = setup({ selectedId: 'A' }); await flushPromises();
    await w.find('.launch-area .launch-btn').trigger('click');
    await flushPromises();
    expect(pushToast).toHaveBeenCalledWith(en.account.gameRunning);
  });

  it('i18n parity: new keys are non-empty across all locales', () => {
    for (const loc of [en, tw, cn] as Record<string, any>[]) {
      for (const k of ['launching', 'play_as', 'launch_failed']) {
        expect(((loc.buttons?.[k] ?? '') as string).length).toBeGreaterThan(0);
      }
      expect(((loc.account?.currentlyLoggedIn ?? '') as string).length).toBeGreaterThan(0);
    }
  });
});

describe('BottomBar progress clamp', () => {
  beforeEach(() => { isRunning.mockReset(); launch.mockReset(); });

  it('clamps progress width to 100% when current > total (backend over-count defense)', async () => {
    const w = setup({ selectedId: 'A' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'download', current: 150, total: 100 },
    } as never;
    await flushPromises();
    const fill = w.find('.progress-btn .fill');
    expect(fill.exists()).toBe(true);
    expect(fill.attributes('style')).toContain('width: 100%');
  });
});

describe('BottomBar patching stage label', () => {
  beforeEach(() => { isRunning.mockReset(); launch.mockReset(); });

  it('interpolates {x}/{y} from in_flight current/total during the patching stage (InFlightOp has no .params)', async () => {
    const w = setup({ selectedId: 'A' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'apply', stage: 'patching', current: 7, total: 42 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn .label');
    expect(label.exists()).toBe(true);
    expect(label.text()).toContain('7');
    expect(label.text()).toContain('42');
    // Guard against the pre-fix regression: no blank/undefined interpolation.
    expect(label.text()).not.toContain('undefined');
  });
});

// 2026-09-29 real-run regression: Sophon emits stage "download" (no such
// update.stage.* key) so stageLabel returned the raw key and the percentage
// fallback never ran; the apply stage's static label carried no numbers.
describe('BottomBar download/apply progress labels', () => {
  beforeEach(() => { isRunning.mockReset(); launch.mockReset(); });

  it('download stage shows percentage and sizes, never the raw key', async () => {
    const w = setup({ selectedId: 'A', locale: 'zh-TW' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'download', stage: 'download', current: 50 * 1024 * 1024, total: 100 * 1024 * 1024 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn.update .label');
    expect(label.exists()).toBe(true);
    expect(label.text()).toBe('下載中 50% (50 MB / 100 MB)');
    expect(label.text()).not.toContain('update.stage.download');
  });

  it('download with unknown total shows percentage only', async () => {
    const w = setup({ selectedId: 'A', locale: 'zh-TW' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'download', stage: 'download', current: 0, total: 0 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn.update .label');
    expect(label.exists()).toBe(true);
    expect(label.text()).toBe('下載中 0%');
    expect(label.text()).not.toContain('MB');
  });

  it('predownload shows percentage and sizes, never the raw key', async () => {
    const w = setup({ selectedId: 'A', locale: 'zh-TW' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'predownload', phase: 'download', stage: 'download', current: 1.5 * 1024 ** 3, total: 3 * 1024 ** 3 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn.predl .label');
    expect(label.exists()).toBe(true);
    expect(label.text()).toBe('預下載 50% (1.5 GB / 3.0 GB)');
    expect(label.text()).not.toContain('update.stage.download');
  });

  it('applying stage shows 套用中 pct% (x/y)', async () => {
    const w = setup({ selectedId: 'A', locale: 'zh-TW' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'apply', stage: 'applying', current: 1042, total: 2084 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn.update .label');
    expect(label.exists()).toBe(true);
    expect(label.text().trim()).toBe('套用中 50% (1042/2084)');
  });

  it('applying with unknown total falls back to the static stage label', async () => {
    const w = setup({ selectedId: 'A', locale: 'zh-TW' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'apply', stage: 'applying', current: 0, total: 0 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn.update .label');
    expect(label.exists()).toBe(true);
    expect(label.text()).toContain('套用更新（不可中斷）');
    expect(label.text()).not.toContain('%');
    expect(label.text()).not.toContain('NaN');
  });

  it('unknown stage falls back to phase label', async () => {
    const w = setup({ selectedId: 'A', locale: 'zh-TW' });
    const updates = useUpdatesStore();
    updates.byGame[GID] = {
      in_flight: { kind: 'update', phase: 'download', stage: 'no_such_stage', current: 25 * 1024 * 1024, total: 100 * 1024 * 1024 },
    } as never;
    await flushPromises();
    const label = w.find('.progress-btn.update .label');
    expect(label.exists()).toBe(true);
    expect(label.text()).toContain('25%');
    expect(label.text()).not.toContain('update.stage.no_such_stage');
  });
});

// Final review I-1: the elevate button for an interrupted bundle install must
// relaunch with --elevate-install-bundle, never --elevate-update.
describe('BottomBar relaunch as administrator', () => {
  beforeEach(() => { setLang('en'); [isRunning, isElevated, relaunchElevated, relaunchForBundle].forEach((m) => m.mockReset()); });

  async function clickElevate(params: Record<string, string>) {
    isElevated.mockResolvedValue(false);
    const w = setup({ selectedId: 'A' });
    useUpdatesStore().byGame[GID] = { last_error: { code: 'permission_denied', retryable: true, params } } as never;
    await flushPromises();
    await w.find('.elevate-btn').trigger('click');
    await flushPromises();
    return w;
  }

  it('bundle permission_denied → RelaunchElevatedForBundle with bundle copy', async () => {
    const w = await clickElevate({ game: GID, bundle: 'SD' });
    expect(relaunchForBundle).toHaveBeenCalledWith(GID, 'SD');
    expect(relaunchElevated).not.toHaveBeenCalled();
    expect(w.text()).toContain(en.bundle.permission_denied_install.replace('{bundle}', 'SD'));
  });

  it('plain permission_denied → RelaunchElevated', async () => {
    await clickElevate({ game: GID });
    expect(relaunchElevated).toHaveBeenCalledWith(GID);
    expect(relaunchForBundle).not.toHaveBeenCalled();
  });
});
