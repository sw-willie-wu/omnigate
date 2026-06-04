import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
vi.mock('../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
}));

// Capture the gacha:progress handler bind() registers (hoist-safe).
const evt = vi.hoisted(() => ({ progress: undefined as ((gid: string, p: unknown) => void) | undefined }));
vi.mock('../../wailsjs/runtime/runtime', () => ({
  EventsOn: (name: string, fn: (gid: string, p: unknown) => void) => { if (name === 'gacha:progress') evt.progress = fn; },
}));

import { useGachaStore } from '../stores/gacha';

const blankSummary = { supported: true, uid: '', totalPulls: 0, perBanner: {}, recentHeadline: [], pity: [], distribution: [] };

describe('gacha store', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); });

  it('load reads summary lazily and caches', async () => {
    getSummary.mockResolvedValue({ ...blankSummary, totalPulls: 5 });
    const s = useGachaStore();
    await s.load('hypergryph/endfield');
    expect(s.stateFor('hypergryph/endfield').summary?.totalPulls).toBe(5);
    await s.load('hypergryph/endfield');
    expect(getSummary).toHaveBeenCalledTimes(1);
  });

  it('refresh calls RefreshGacha and replaces summary', async () => {
    refreshGacha.mockResolvedValue({ ...blankSummary, totalPulls: 9 });
    const s = useGachaStore();
    await s.refresh('hypergryph/endfield');
    expect(s.stateFor('hypergryph/endfield').summary?.totalPulls).toBe(9);
  });

  it('refresh maps gacha_url error to errKind=url', async () => {
    refreshGacha.mockRejectedValue(new Error('gacha history url unavailable'));
    const s = useGachaStore();
    await s.refresh('hypergryph/endfield');
    expect(s.stateFor('hypergryph/endfield').errKind).toBe('url');
  });

  it('bind routes gacha:progress events into the matching gid', async () => {
    getSummary.mockResolvedValue({ ...blankSummary, totalPulls: 1 });
    const s = useGachaStore();
    await s.load('hypergryph/endfield'); // create the byGid entry
    s.bind();
    expect(evt.progress).toBeTypeOf('function');
    evt.progress!('hypergryph/endfield', { banner: { en: 'Limited' }, page: 3, poolIndex: 2, poolTotal: 4 });
    expect(s.stateFor('hypergryph/endfield').progress?.page).toBe(3);
    // an event for an unknown gid is ignored (no throw)
    evt.progress!('nope/none', { banner: {}, page: 1, poolIndex: 1, poolTotal: 1 });
  });

  it('reset clears cache', async () => {
    getSummary.mockResolvedValue({ ...blankSummary, totalPulls: 1 });
    const s = useGachaStore();
    await s.load('hypergryph/endfield');
    s.reset();
    await s.load('hypergryph/endfield');
    expect(getSummary).toHaveBeenCalledTimes(2);
  });
});
