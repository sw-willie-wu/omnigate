import { describe, it, expect, vi, beforeEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { createApp, defineComponent, h } from 'vue';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const discardRpc = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  DiscardInterrupted: (...a: unknown[]) => discardRpc(...a),
  GetBundleState: vi.fn().mockResolvedValue({ supported: true, active: 'HD', bundles: [], options: [], catalog_stale: false }),
  SetActiveBundle: vi.fn(), InstallBundle: vi.fn(), RemoveBundle: vi.fn(), SetLaunchOption: vi.fn(),
  StartUpdate: vi.fn(), StartPredownload: vi.fn(), CancelInFlight: vi.fn(), ApplyPredownload: vi.fn(),
  RemovePredownload: vi.fn(), DismissError: vi.fn(), ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(),
  CheckForUpdate: vi.fn(), RelaunchElevated: vi.fn(),
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(), GetBackgrounds: vi.fn(),
  SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(), RefreshGame: vi.fn(), GetCustomBackground: vi.fn(),
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));
vi.mock('../useToast', () => ({ pushToast: vi.fn(), registerToast: vi.fn() }));

import { useResumePrompt } from '../useResumePrompt';
import { useUpdatesStore } from '../../stores/updates';

const GID = 'kurogames/wutheringwaves';

function withSetup<T>(fn: () => T): T {
  let out!: T;
  const app = createApp(defineComponent({ setup() { out = fn(); return () => h('div'); } }));
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }));
  app.mount(document.createElement('div'));
  return out;
}

describe('useResumePrompt bundle interruptions', () => {
  beforeEach(() => { setActivePinia(createPinia()); discardRpc.mockReset(); });

  it('uses bundle copy and discards via RPC', async () => {
    useUpdatesStore().byGame[GID] = { last_error: { code: 'interrupted_resume', retryable: true, params: { phase: 'download', bundle: 'SD', version: '3.7.0' } } };
    const { pending, discard } = withSetup(() => useResumePrompt());
    expect(pending.value).toHaveLength(1);
    expect(pending.value[0].message).toContain('SD');
    expect(pending.value[0].bundle).toBe('SD');
    expect(pending.value[0].resumeLabel).toBe(en.bundle.resume);
    expect(pending.value[0].dismissLabel).toBe(en.bundle.later);
    await discard(GID);
    expect(discardRpc).toHaveBeenCalledWith(GID);
  });

  it('keeps the generic copy for non-bundle interruptions', () => {
    useUpdatesStore().byGame[GID] = { last_error: { code: 'interrupted_resume', retryable: true, params: { phase: 'download' } } };
    const { pending } = withSetup(() => useResumePrompt());
    expect(pending.value[0].bundle).toBeUndefined();
    expect(pending.value[0].message).toBe(en.update.errors.interrupted_resume_download);
    expect(pending.value[0].resumeLabel).toBe(en.buttons.confirm);
    expect(pending.value[0].dismissLabel).toBe(en.buttons.cancel);
  });
});
