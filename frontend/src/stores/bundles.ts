import { defineStore } from 'pinia';
import {
  GetBundleState, SetActiveBundle, InstallBundle, RemoveBundle, SetLaunchOption, DiscardInterrupted,
  IsElevated, RelaunchElevatedForBundle,
} from '../../wailsjs/go/app/App';
import { pushToast } from '../composables/useToast';
import { i18n } from '../i18n';
import type { UpdateError } from './updates';

// Mirrors internal/app BundleState / BundleRow / LaunchOptionRow (spec §6.2).
export type BundleRow = {
  name: string;
  display_name: Record<string, string>;
  installed: boolean;
  pending: boolean;
  version?: string;
  size_bytes: number;
  launch_supported: boolean;
  removable: boolean;
};
export type LaunchOptionRow = { cmd: string; label: Record<string, string>; enabled: boolean; default: boolean };
export type BundleState = {
  supported: boolean;
  active: string;
  bundles: BundleRow[] | null;
  options: LaunchOptionRow[] | null;
  catalog_stale: boolean;
  error?: UpdateError | null;
};

function isResidualBusy(e: UpdateError): boolean {
  return e.code === 'bundle_busy' && e.params?.reason === 'residual';
}

// errorText mirrors BottomBar's errorLabel lookup (spec §6.9): codes live in
// update.error.* and update.errors.*; unknown codes fall back to the generic
// internal template so a raw i18n key never reaches a toast.
export function errorText(e: UpdateError): string {
  const { t, te } = i18n.global;
  if (isResidualBusy(e)) {
    return e.params?.bundle
      ? t('update.errors.bundle_busy_residual', e.params)
      : t('update.errors.bundle_busy_residual_update');
  }
  const params = e.params ?? {};
  if (te(`update.error.${e.code}`)) return t(`update.error.${e.code}`, params);
  if (te(`update.errors.${e.code}`)) return t(`update.errors.${e.code}`, params);
  return t('update.errors.internal', { detail: params.detail || e.code });
}

// relaunchForBundle: restart omnigate elevated to continue installing `name`.
// On success this instance quits; a rejection (UAC declined or real error) toasts.
async function relaunchForBundle(gid: string, name: string): Promise<void> {
  try {
    await RelaunchElevatedForBundle(gid, name);
  } catch (e) {
    const msg = String((e as Error)?.message ?? e);
    pushToast(msg.includes('uac_declined') ? i18n.global.t('update.elevate_cancelled') : msg);
  }
}

export const useBundlesStore = defineStore('bundles', {
  state: () => ({ byGame: {} as Record<string, BundleState> }),
  actions: {
    apply(gid: string, st: BundleState): BundleState {
      this.byGame[gid] = st;
      const e = st.error;
      if (e) {
        if (isResidualBusy(e)) {
          // Residual sidecar blocks bundle ops: offer "放棄" right on the toast.
          pushToast(errorText(e), {
            retryable: true,
            retryLabel: i18n.global.t('bundle.discard'),
            onRetry: () => void this.discardInterrupted(gid),
          });
        } else {
          pushToast(errorText(e));
        }
      }
      return st;
    },
    async load(gid: string) { return this.apply(gid, (await GetBundleState(gid)) as BundleState); },
    async setActive(gid: string, name: string) { return this.apply(gid, (await SetActiveBundle(gid, name)) as BundleState); },
    async install(gid: string, name: string) {
      const st = (await InstallBundle(gid, name)) as BundleState;
      if (st.error?.code === 'permission_denied') {
        // IsElevated rejecting counts as elevated (same as BottomBar) → no button, no loop.
        let elevated = true;
        try { elevated = await IsElevated(); } catch { elevated = true; }
        if (!elevated) {
          this.byGame[gid] = st;
          pushToast(errorText(st.error), {
            retryable: true,
            retryLabel: i18n.global.t('buttons.relaunch_admin'),
            onRetry: () => void relaunchForBundle(gid, name),
          });
          return st;
        }
      }
      return this.apply(gid, st);
    },
    async remove(gid: string, name: string) { return this.apply(gid, (await RemoveBundle(gid, name)) as BundleState); },
    async setOption(gid: string, cmd: string, enabled: boolean) {
      return this.apply(gid, (await SetLaunchOption(gid, cmd, enabled)) as BundleState);
    },
    async discardInterrupted(gid: string) {
      await DiscardInterrupted(gid);
      await this.load(gid);
    },
  },
});
