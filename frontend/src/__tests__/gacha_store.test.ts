import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
vi.mock('../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
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

  it('reset clears cache', async () => {
    getSummary.mockResolvedValue({ ...blankSummary, totalPulls: 1 });
    const s = useGachaStore();
    await s.load('hypergryph/endfield');
    s.reset();
    await s.load('hypergryph/endfield');
    expect(getSummary).toHaveBeenCalledTimes(2);
  });
});
