import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

import GachaBoard from '../GachaBoard.vue';
import { useGachaStore } from '../../stores/gacha';

function mountBoard() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(GachaBoard, { props: { gid: 'hypergryph/endfield' }, global: { plugins: [i18n] } });
}
const base = {
  supported: true, uid: 'u1', totalPulls: 12, perBanner: { special: 8, standard: 4 }, spendEst: 1200, currency: 'primogem',
  headlineCnt: 2, headlineByType: { char: 2 }, avgPity: 6, expectedPity: 62.5, luckScore: 80, winRate5050: null, worstPull: 9,
  pity: [{ key: 'special', label: { en: 'Limited' }, current: 10, cap: 80, nearPity: false }],
  distribution: [1, 0, 0, 0, 0, 0, 0, 0, 1], recentHeadline: [{ name: 'Alpha', itemType: 'char', bannerKey: 'special', time: '2026-06-01', count: 4 }],
};

describe('GachaBoard', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); });

  it('renders the dashboard sections from summary', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-cards').exists()).toBe(true);
    expect(w.find('.donut').exists()).toBe(true);
    expect(w.find('.gacha-pity').exists()).toBe(true);
    expect(w.find('.gacha-dist').exists()).toBe(true);
    expect(w.find('.gacha-recent').exists()).toBe(true);
    expect(w.text()).toContain('12');
    expect(w.text()).toContain('Alpha');
  });

  it('binds the luck donut via inline conic-gradient + score', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    const style = w.find('.donut').attributes('style') ?? '';
    expect(style).toContain('conic-gradient');
    expect(style).toContain('80%');
    expect(w.find('.donut-score').text()).toBe('80');
  });

  it('resolves recent-card banner label from pity[], not the raw key', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    const banner = w.find('.recent-item .r-banner').text();
    expect(banner).toBe('Limited');
    expect(banner).not.toBe('special');
  });

  it('shows the universal donut trio and no 50/50 win-rate line', async () => {
    // HoYo-shaped (cap 90) and Endfield-shaped (cap 80): both winRate5050 null.
    for (const cap of [90, 80]) {
      getSummary.mockResolvedValue({ ...base, pity: [{ key: 'special', label: { en: 'Limited' }, current: 10, cap, nearPity: false }] });
      const w = mountBoard(); await flushPromises();
      expect(w.find('.luck-trio').exists()).toBe(true);
      expect(w.text()).not.toContain('%'); // no win-rate percentage rendered
    }
  });

  it('marks near-pity bars and highlights max distribution buckets', async () => {
    getSummary.mockResolvedValue({ ...base, pity: [{ key: 'special', label: { en: 'Limited' }, current: 76, cap: 80, nearPity: true }] });
    const w = mountBoard(); await flushPromises();
    expect(w.find('.pity-row.near').exists()).toBe(true);
    // distribution [1,...,1] → two buckets tie at max → two .hot bars
    expect(w.findAll('.dist-bar.hot').length).toBe(2);
  });

  it('shows a skeleton while the summary is pending', async () => {
    getSummary.mockReturnValue(new Promise(() => {})); // never resolves → stays loading
    const w = mountBoard();
    await flushPromises(); // let onMounted's load() set loading + re-render (fetch stays pending)
    expect(w.find('.gacha-skeleton').exists()).toBe(true);
  });

  it('shows empty state when no data', async () => {
    getSummary.mockResolvedValue({ ...base, totalPulls: 0, headlineCnt: 0, recentHeadline: [] });
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-empty').exists()).toBe(true);
  });

  it('shows unsupported state', async () => {
    getSummary.mockResolvedValue({ ...base, supported: false });
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-unsupported').exists()).toBe(true);
  });

  it('shows the other-error panel when initial load fails', async () => {
    getSummary.mockRejectedValue(new Error('boom'));
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-error').exists()).toBe(true);
  });

  it('shows consumed stones with the localized currency name (no NTD/估算)', async () => {
    getSummary.mockResolvedValue({ ...base, currency: 'primogem', spendEst: 1200 });
    const w = mountBoard(); await flushPromises();
    expect(w.text()).toContain('Primogems');
    expect(w.text()).toContain('1,200');
    expect(w.text()).not.toContain('NT$');
  });

  it('hides pity rows for pools with no records', async () => {
    getSummary.mockResolvedValue({
      ...base,
      perBanner: { special: 8 }, // 'beginner' absent → 0 records → hidden
      pity: [
        { key: 'special', label: { en: 'Limited' }, current: 10, cap: 80, nearPity: false },
        { key: 'beginner', label: { en: 'Beginner' }, current: 0, cap: 90, nearPity: false },
      ],
    });
    const w = mountBoard(); await flushPromises();
    expect(w.findAll('.pity-row').length).toBe(1);
    expect(w.find('.gacha-pity').text()).toContain('Limited');
    expect(w.find('.gacha-pity').text()).not.toContain('Beginner');
  });

  it('shows spinner + live progress text during refresh', async () => {
    // Pre-seed a loading+progress state; onMounted load() early-returns (loading).
    const store = useGachaStore();
    store.byGid['hypergryph/endfield'] = {
      summary: null, loading: true, errKind: null, loaded: false,
      progress: { banner: { en: 'Limited' }, page: 5, poolIndex: 2, poolTotal: 4 },
    };
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-spinner').exists()).toBe(true);
    expect(w.find('.gacha-progress-text').text()).toContain('Limited');
    expect(w.find('.gacha-progress-text').text()).toContain('5');
  });

  it('shows generic loading (spinner) when no progress tick yet', async () => {
    getSummary.mockReturnValue(new Promise(() => {})); // pending
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-spinner').exists()).toBe(true);
    expect(w.find('.gacha-skeleton').exists()).toBe(true);
  });

  it('shows url-reopen guidance when refresh errKind=url', async () => {
    getSummary.mockResolvedValue(base);
    refreshGacha.mockRejectedValue(new Error('gacha history url unavailable'));
    const w = mountBoard(); await flushPromises();
    await w.find('.gacha-refresh').trigger('click'); await flushPromises();
    expect(w.find('.gacha-url-hint').exists()).toBe(true);
  });
});
