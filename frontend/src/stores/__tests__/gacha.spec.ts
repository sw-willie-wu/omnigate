import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

import { useGachaStore } from '../gacha';

describe('gacha store', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); });

  it('reload re-reads GetGachaSummary even when already loaded', async () => {
    getSummary.mockResolvedValue({ supported: true, totalPulls: 0 });
    const s = useGachaStore();
    await s.load('g');
    expect(getSummary).toHaveBeenCalledTimes(1);
    await s.load('g'); // guarded → no-op
    expect(getSummary).toHaveBeenCalledTimes(1);
    await s.reload('g'); // forced
    expect(getSummary).toHaveBeenCalledTimes(2);
  });

  it('classifies wrong-account and url-expired refresh errors by message token', async () => {
    getSummary.mockResolvedValue({ supported: true, totalPulls: 1 });
    const s = useGachaStore();
    refreshGacha.mockRejectedValue(new Error('gacha record belongs to a different account'));
    await s.refresh('g');
    expect(s.stateFor('g').errKind).toBe('wrong_account');
    refreshGacha.mockRejectedValue(new Error('gacha convene url expired'));
    await s.refresh('g');
    expect(s.stateFor('g').errKind).toBe('url_expired');
  });

  it('still classifies the legacy url-unavailable error as url', async () => {
    getSummary.mockResolvedValue({ supported: true, totalPulls: 1 });
    const s = useGachaStore();
    refreshGacha.mockRejectedValue(new Error('gacha history url unavailable'));
    await s.refresh('g');
    expect(s.stateFor('g').errKind).toBe('url');
  });
});
