import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

let handler: ((gid: string, snap: unknown) => void) | null = null;
let raf: FrameRequestCallback | null = null; // captured; flushed manually after each event (see src/__tests__/updates_store.test.ts:56)
const getBundleState = vi.fn();
vi.mock('../../../wailsjs/runtime/runtime', () => ({
  EventsOn: (_name: string, cb: (gid: string, snap: unknown) => void) => { handler = cb; },
}));
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetBundleState: (...a: unknown[]) => getBundleState(...a),
  SetActiveBundle: vi.fn(), InstallBundle: vi.fn(), RemoveBundle: vi.fn(), SetLaunchOption: vi.fn(), DiscardInterrupted: vi.fn(),
  StartUpdate: vi.fn(), StartPredownload: vi.fn(), CancelInFlight: vi.fn(), ApplyPredownload: vi.fn(),
  RemovePredownload: vi.fn(), DismissError: vi.fn(), ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(),
  CheckForUpdate: vi.fn(), RelaunchElevated: vi.fn(),
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(), GetBackgrounds: vi.fn(),
  SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(), RefreshGame: vi.fn(), GetCustomBackground: vi.fn(),
}));
vi.mock('../../composables/useToast', () => ({ pushToast: vi.fn(), registerToast: vi.fn() }));

import { useUpdatesStore } from '../updates';

const GID = 'kurogames/wutheringwaves';

describe('updates store bundle reload', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getBundleState.mockReset().mockResolvedValue({ supported: true, active: 'SD', bundles: [], options: [], catalog_stale: false });
    raf = null;
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { raf = cb; return 1; });
  });
  afterEach(() => vi.unstubAllGlobals());

  it('reloads bundle state when a bundle in-flight ends, even with last_error', () => {
    const store = useUpdatesStore();
    store.bind();
    handler!(GID, { in_flight: { kind: 'update', phase: 'download', bundle: 'SD', current: 1, total: 2, version: '3.7.0', started_at: '' } });
    raf!(0);
    handler!(GID, { last_error: { code: 'network', retryable: true } });
    raf!(0);
    expect(getBundleState).toHaveBeenCalledTimes(1);
    expect(getBundleState).toHaveBeenCalledWith(GID);
  });

  it('does not reload for a normal update', () => {
    const store = useUpdatesStore();
    store.bind();
    handler!(GID, { in_flight: { kind: 'update', phase: 'download', current: 1, total: 2, version: '3.7.0', started_at: '' } });
    raf!(0);
    handler!(GID, {});
    raf!(0);
    expect(getBundleState).not.toHaveBeenCalled();
  });
});
