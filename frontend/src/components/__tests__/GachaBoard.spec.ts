import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
const listAccounts = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
  ListGameAccounts: (...a: unknown[]) => listAccounts(...a),
  SetAccountLabel: vi.fn(),
  StartGachaLink: vi.fn(),
  SetGachaCredential: vi.fn(),
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
  distribution: [1, 0, 0, 0, 0, 0, 0, 0, 1],
  recentHeadline: [{ name: 'Alpha', itemType: 'char', bannerKey: 'special', time: '2026-06-01', count: 4, rank: 5 }],
  highlights: [
    { name: 'Alpha', itemType: 'char', bannerKey: 'character', time: '2026-06-03', count: 12, rank: 5 },
    { name: 'Blade', itemType: 'weapon', bannerKey: 'weapon', time: '2026-06-02', count: 28, rank: 5 },
    { name: 'Pip', itemType: 'char', bannerKey: 'character', time: '2026-06-01', count: 7, rank: 4 },
  ],
};

describe('GachaBoard', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); listAccounts.mockReset(); listAccounts.mockResolvedValue([]); });

  it('renders the dashboard sections from summary', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-cards').exists()).toBe(true);
    expect(w.find('.donut').exists()).toBe(true);
    expect(w.find('.gacha-pity').exists()).toBe(true);
    expect(w.find('.gacha-dist').exists()).toBe(true);
    expect(w.find('.gacha-hl').exists()).toBe(true);
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

  it('splits high-star records into character | weapon columns and toggles ranks', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    const cols = w.findAll('.hl-col');
    expect(cols.length).toBe(2);
    // default shows only the top rank (5★): Alpha (char) left, Blade (weapon) right; Pip (4★) hidden
    expect(cols[0].text()).toContain('Alpha');
    expect(cols[1].text()).toContain('Blade');
    expect(w.text()).not.toContain('Pip');
    // toggling 4★ on reveals the 4★ entry
    const four = w.findAll('.hl-rank').find((b) => b.text() === '4★');
    expect(four).toBeTruthy();
    await four!.trigger('click'); await flushPromises();
    expect(w.text()).toContain('Pip');
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

  it('shows the play-first prompt (no refresh button) when active uid is unknown', async () => {
    getSummary.mockResolvedValue({ ...base, totalPulls: 0, headlineCnt: 0, recentHeadline: [], activeUnknown: true });
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-play-first').exists()).toBe(true);
    expect(w.find('.gacha-refresh').exists()).toBe(false);
  });

  it('shows wrong-account guidance when refresh reports a mismatch', async () => {
    getSummary.mockResolvedValue(base);
    refreshGacha.mockRejectedValue(new Error('gacha record belongs to a different account'));
    const w = mountBoard(); await flushPromises();
    await w.find('.gacha-refresh').trigger('click'); await flushPromises();
    expect(w.find('.gacha-wrong-account').exists()).toBe(true);
  });

  it('shows url-reopen guidance with the url-expired copy on a url_expired error', async () => {
    getSummary.mockResolvedValue(base);
    refreshGacha.mockRejectedValue(new Error('gacha convene url expired'));
    const w = mountBoard(); await flushPromises();
    await w.find('.gacha-refresh').trigger('click'); await flushPromises();
    expect(w.find('.gacha-url-hint').exists()).toBe(true);
    // distinct from the legacy url_hint copy: url_expired text mentions "convene".
    expect(w.find('.gacha-url-hint').text()).toContain('convene');
  });

  it('reloads with the newly selected account id', async () => {
    listAccounts.mockResolvedValue([
      { id: 'A', uid: 'uA', label: '', email: 'a', username: 'UA', active: true },
      { id: 'B', uid: 'uB', label: '', email: 'b', username: 'UB', active: false },
    ]);
    getSummary.mockResolvedValue(base);
    const { useAccountStore } = await import('../../stores/account');
    const w = mountBoard(); await flushPromises();
    const acct = useAccountStore();
    await acct.load('hypergryph/endfield'); // populate; selected defaults to A
    getSummary.mockClear();
    acct.select('hypergryph/endfield', 'B');
    await flushPromises();
    expect(getSummary).toHaveBeenCalledWith('hypergryph/endfield', 'B');
    w.unmount();
  });

  it('renders the link panel when GetGachaSummary reports credential required', async () => {
    getSummary.mockRejectedValue(new Error('gacha credential required'));
    const w = mountBoard(); await flushPromises();
    expect(w.find('[data-test="gacha-link-login"]').exists()).toBe(true);
    expect(w.text()).toContain('Link your Gryphline'); // en gacha.link.title
  });

  it('has the 3 new gacha keys non-empty in every locale (i18n parity)', async () => {
    const en = (await import('../../locales/en.json')).default as Record<string, any>;
    const tw = (await import('../../locales/zh-TW.json')).default as Record<string, any>;
    const cn = (await import('../../locales/zh-CN.json')).default as Record<string, any>;
    for (const loc of [en, tw, cn]) {
      for (const k of ['play_first', 'wrong_account', 'url_expired']) {
        expect(((loc.gacha?.[k] ?? '') as string).length).toBeGreaterThan(0);
      }
      for (const k of ['title', 'login', 'step1', 'step2', 'step3', 'paste', 'pasteLabel', 'rearm']) {
        expect(((loc.gacha?.link?.[k] ?? '') as string).length).toBeGreaterThan(0);
      }
      expect(((loc.gacha?.currency?.endfield_oroberyl ?? '') as string).length).toBeGreaterThan(0);
    }
  });
});
