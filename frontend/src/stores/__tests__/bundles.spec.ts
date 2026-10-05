import { describe, it, expect, vi, beforeEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import en from '../../locales/en.json';

const installRpc = vi.fn();
const isElevated = vi.fn();
const relaunchForBundle = vi.fn();
const pushToast = vi.fn();
const removeRpc = vi.fn();
const discardRpc = vi.fn();
const getState = vi.fn();
const relaunchAsAdmin = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetBundleState: (...a: unknown[]) => getState(...a),
  SetActiveBundle: vi.fn(),
  InstallBundle: (...a: unknown[]) => installRpc(...a),
  RemoveBundle: (...a: unknown[]) => removeRpc(...a),
  SetLaunchOption: vi.fn(),
  DiscardInterrupted: (...a: unknown[]) => discardRpc(...a),
  IsElevated: (...a: unknown[]) => isElevated(...a),
  RelaunchElevatedForBundle: (...a: unknown[]) => relaunchForBundle(...a),
  RelaunchAsAdmin: (...a: unknown[]) => relaunchAsAdmin(...a),
}));
vi.mock('../../composables/useToast', () => ({ pushToast: (...a: unknown[]) => pushToast(...a), registerToast: vi.fn() }));

import { useBundlesStore, errorText } from '../bundles';
import { setLang } from '../../i18n';

const GID = 'kurogames/wutheringwaves';
const denied = { supported: true, active: 'HD', bundles: [], options: [], catalog_stale: false, error: { code: 'permission_denied', retryable: false, params: { game: GID } } };

describe('bundles store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    setLang('en');
    [installRpc, isElevated, relaunchForBundle, pushToast, removeRpc, discardRpc, getState, relaunchAsAdmin].forEach((m) => m.mockReset());
  });

  it('errorText resolves update.error.* codes (not a raw key)', () => {
    const txt = errorText({ code: 'permission_denied', retryable: false, params: { game: 'x' } } as never);
    expect(txt).toBe(en.update.error.permission_denied.replace('{game}', 'x'));
  });

  it('errorText falls back to update.errors.internal for unknown codes', () => {
    const txt = errorText({ code: 'no_such_code_xyz', retryable: false } as never);
    expect(txt).toBe(en.update.errors.internal.replace('{detail}', 'no_such_code_xyz'));
  });

  it('permission_denied when not elevated: one toast with relaunch button that calls the RPC', async () => {
    installRpc.mockResolvedValue(denied);
    isElevated.mockResolvedValue(false);
    relaunchForBundle.mockResolvedValue(undefined);
    await useBundlesStore().install(GID, 'SD');
    expect(pushToast).toHaveBeenCalledTimes(1);
    const opts = pushToast.mock.calls[0][1];
    expect(opts.retryLabel).toBe(en.buttons.relaunch_admin);
    opts.onRetry();
    expect(relaunchForBundle).toHaveBeenCalledWith(GID, 'SD');
  });

  it('relaunch UAC declined → elevate_cancelled toast', async () => {
    installRpc.mockResolvedValue(denied);
    isElevated.mockResolvedValue(false);
    relaunchForBundle.mockRejectedValue('uac_declined');
    await useBundlesStore().install(GID, 'SD');
    pushToast.mock.calls[0][1].onRetry();
    await vi.waitFor(() => expect(pushToast).toHaveBeenCalledTimes(2));
    expect(pushToast.mock.calls[1][0]).toBe(en.update.elevate_cancelled);
  });

  it('permission_denied when already elevated: one plain toast', async () => {
    installRpc.mockResolvedValue(denied);
    isElevated.mockResolvedValue(true);
    await useBundlesStore().install(GID, 'SD');
    expect(pushToast).toHaveBeenCalledTimes(1);
    expect(pushToast.mock.calls[0][1]?.retryLabel).toBeUndefined();
  });

  it('permission_denied when IsElevated rejects: treated as elevated, one plain toast', async () => {
    installRpc.mockResolvedValue(denied);
    isElevated.mockRejectedValue(new Error('x'));
    await useBundlesStore().install(GID, 'SD');
    expect(pushToast).toHaveBeenCalledTimes(1);
    expect(pushToast.mock.calls[0][1]?.retryLabel).toBeUndefined();
  });

  it('other install errors keep a single plain toast and do not query IsElevated', async () => {
    installRpc.mockResolvedValue({ ...denied, error: { code: 'bundle_installed', retryable: false, params: { bundle: 'SD' } } });
    await useBundlesStore().install(GID, 'SD');
    expect(pushToast).toHaveBeenCalledTimes(1);
    expect(isElevated).not.toHaveBeenCalled();
  });

  it('errorText uses bundle copy for permission_denied carrying a bundle', () => {
    expect(errorText({ code: 'permission_denied', retryable: true, params: { game: GID, bundle: 'SD' } } as never))
      .toBe(en.bundle.permission_denied_install.replace('{bundle}', 'SD'));
    expect(errorText({ code: 'permission_denied', retryable: true, params: { game: GID, bundle: 'SD', op: 'remove' } } as never))
      .toBe(en.bundle.permission_denied_remove.replace('{bundle}', 'SD'));
  });

  it('remove permission_denied when not elevated: relaunch button uses the plain admin relaunch', async () => {
    removeRpc.mockResolvedValue({ ...denied, error: { code: 'permission_denied', retryable: true, params: { game: GID, bundle: 'SD', op: 'remove' } } });
    isElevated.mockResolvedValue(false);
    relaunchAsAdmin.mockResolvedValue(undefined);
    await useBundlesStore().remove(GID, 'SD');
    expect(pushToast).toHaveBeenCalledTimes(1);
    const opts = pushToast.mock.calls[0][1];
    expect(opts.retryLabel).toBe(en.buttons.relaunch_admin);
    opts.onRetry();
    await vi.waitFor(() => expect(relaunchAsAdmin).toHaveBeenCalledTimes(1));
    expect(relaunchForBundle).not.toHaveBeenCalled();
  });

  it('discardInterrupted failure toasts instead of rejecting and still reloads', async () => {
    discardRpc.mockRejectedValue(new Error('locked'));
    getState.mockResolvedValue({ supported: true, active: 'HD', bundles: [], options: [], catalog_stale: false });
    await expect(useBundlesStore().discardInterrupted(GID)).resolves.toBeUndefined();
    expect(pushToast).toHaveBeenCalledWith(en.bundle.discard_failed.replace('{detail}', 'locked'));
    expect(getState).toHaveBeenCalledWith(GID);
  });
});
