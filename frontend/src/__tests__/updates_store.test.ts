import { describe, it, test, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

// Mock the wails RPC + EventsOn BEFORE importing the store
vi.mock('../../wailsjs/go/app/App', () => ({
  StartUpdate: vi.fn(),
  StartPredownload: vi.fn(),
  CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(),
  RemovePredownload: vi.fn(),
  DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(),
  UpdateStatusAll: vi.fn(async () => ({})),
  RelaunchElevated: vi.fn(),
}));

let eventHandler: ((gameID: string, snap: any) => void) | null = null;
vi.mock('../../wailsjs/runtime/runtime', () => ({
  EventsOn: (name: string, fn: any) => {
    if (name === 'update:changed') eventHandler = fn;
  },
}));

// Mock the games store to avoid circular dependency
vi.mock('../stores/games', () => ({
  useGamesStore: vi.fn(() => ({
    refreshVersionFor: vi.fn(),
    loadAssetsFor: vi.fn(),
  })),
}));

import { useUpdatesStore } from '../stores/updates';
import { RelaunchElevated } from '../../wailsjs/go/app/App';

describe('updates store relaunchElevated', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('calls the RelaunchElevated binding with the gid', async () => {
    const store = useUpdatesStore();
    await store.relaunchElevated('kurogames/wutheringwaves');
    expect(RelaunchElevated).toHaveBeenCalledWith('kurogames/wutheringwaves');
  });
});

describe('updates store rAF batching', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    eventHandler = null;
  });

  it('latest-wins on multiple events for same gameID', async () => {
    const store = useUpdatesStore();

    // Capture rAF callback instead of executing immediately
    let rafCallback: (() => void) | null = null;
    global.requestAnimationFrame = ((cb: any) => {
      rafCallback = cb;
      return 0;
    }) as any;

    store.bind();
    eventHandler!('g1', { in_flight: { current: 100 } });
    eventHandler!('g1', { in_flight: { current: 200 } });
    eventHandler!('g1', { in_flight: { current: 300 } });

    // Verify batching: pendingPatches accumulated
    expect(store.pendingPatches['g1'].in_flight.current).toBe(300);

    // Execute rAF callback to flush patches
    rafCallback!();

    // After rAF flush, byGame should have latest value
    expect(store.byGame['g1'].in_flight.current).toBe(300);
  });
});

describe('updates store bell entries', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  test('predl_complete bell entry can be dismissed', () => {
    const store = useUpdatesStore();
    // Push a predl_complete bell entry directly (store has no addBellEntry; dismiss() is the action)
    const entry = { kind: 'predl_complete' as const, gid: 'hoyoverse/genshin', version: '5.7.0', id: 'bell-1' };
    store.bellEntries.push(entry);
    expect(store.bellEntries).toHaveLength(1);
    store.dismiss(store.bellEntries[0].id);
    expect(store.bellEntries).toHaveLength(0);
  });

  test('config_writeback_warning bell entry can be dismissed', () => {
    const store = useUpdatesStore();
    const entry = { kind: 'config_writeback_warning' as const, gid: 'hoyoverse/genshin', version: '5.7.0', id: 'bell-2' };
    store.bellEntries.push(entry);
    expect(store.bellEntries).toHaveLength(1);
    store.dismiss(store.bellEntries[0].id);
    expect(store.bellEntries).toHaveLength(0);
  });
});
