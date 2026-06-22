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
    { name: 'Alpha', itemType: 'char', bannerKey: 'character', time: '2026-06-03', count: 12, rank: 5, off: true, limited: true },
    { name: 'Blade', itemType: 'weapon', bannerKey: 'weapon', time: '2026-06-02', count: 28, rank: 5, off: true, limited: true },
    { name: 'Pip', itemType: 'char', bannerKey: 'character', time: '2026-06-01', count: 7, rank: 4, off: false, limited: true },
  ],
};

describe('GachaBoard', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); listAccounts.mockReset(); listAccounts.mockResolvedValue([]); });

  it('renders the dashboard sections from summary', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-cards').exists()).toBe(true);
    expect(w.findAll('.card').length).toBe(8);
    expect(w.find('.donut').exists()).toBe(true);
    expect(w.find('.gacha-pity').exists()).toBe(false);
    expect(w.find('.hl-pity').exists()).toBe(true);
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

  it('splits high-star records into character | weapon columns, top rank only', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    const cols = w.findAll('.hl-col');
    expect(cols.length).toBe(2);
    // only the top rank (5★) is shown: Alpha (char) left, Blade (weapon) right; Pip (4★) never shown
    expect(cols[0].text()).toContain('Alpha');
    expect(cols[1].text()).toContain('Blade');
    expect(w.text()).not.toContain('Pip');
    // no rank toggle exists anymore
    expect(w.find('.hl-rank').exists()).toBe(false);
  });

  it('shows the universal donut trio', async () => {
    for (const cap of [90, 80]) {
      getSummary.mockResolvedValue({ ...base, pity: [{ key: 'special', label: { en: 'Limited' }, current: 10, cap, nearPity: false }] });
      const w = mountBoard(); await flushPromises();
      expect(w.find('.luck-trio').exists()).toBe(true);
    }
  });

  it('renders the featured (出限定率) card', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    expect(w.text()).toContain('0%'); // base: 2 featured top-rank, both off → 0%
  });

  it('shows expected-value notes on the limited average cards (7/8)', async () => {
    getSummary.mockResolvedValue({
      ...base,
      expectedPity: 54.1,
      expectedFeaturedWeapon: 50, // distinct from char/avg expectations so card 8 is uniquely attributable
      highlights: [
        { name: 'C', itemType: 'char', bannerKey: 'character', time: '2026-06-02', count: 60, rank: 5, off: false, limited: true },
        { name: 'W', itemType: 'weapon', bannerKey: 'weapon', time: '2026-06-01', count: 48, rank: 5, off: false, limited: true },
      ],
    });
    const w = mountBoard(); await flushPromises();
    const txt = w.find('.gacha-cards').text();
    expect(txt).toContain('81.2'); // card 7: 限定角色平均(60) vs 出金×1.5 = 81.15 → "below avg 81.2"
    expect(txt).toContain('50.0'); // card 8: 限定武器平均(48) vs 出限定武器期望 50 → "below avg 50.0" (unique)
  });

  it('dashes empty averages/rate when there are no highlights', async () => {
    getSummary.mockResolvedValue({ ...base, highlights: [] });
    const w = mountBoard(); await flushPromises();
    // scope to the cards (the records area also shows — when empty) so a card-binding regression isn't masked
    expect(w.find('.gacha-cards').text()).toContain('—'); // top=0 → metrics null → cards 5-8 show the em dash
  });

  it('highlights the max distribution buckets', async () => {
    getSummary.mockResolvedValue({ ...base, pity: [{ key: 'special', label: { en: 'Limited' }, current: 76, cap: 80, nearPity: true }] });
    const w = mountBoard(); await flushPromises();
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

  it('shows a pity row for an OPEN one-shot pool but not a CLOSED (spent) one', async () => {
    getSummary.mockResolvedValue({
      ...base,
      perBanner: { special: 8, beginner_choice: 30, beginner: 50 },
      highlights: [
        // beginner already produced its guaranteed 5★ → closed → no pity row, just the record
        { name: 'Bwin', itemType: 'char', bannerKey: 'beginner', time: 't', count: 40, rank: 5, off: false, limited: false },
      ],
      pity: [
        { key: 'special', label: { en: 'Limited' }, current: 10, cap: 80, nearPity: false },
        { key: 'beginner_choice', label: { en: 'Novice Choice' }, current: 30, cap: 80, nearPity: false }, // open: pulled, no 5★ yet
        { key: 'beginner', label: { en: 'Beginner' }, current: 0, cap: 50, nearPity: false },               // closed: got its 5★
      ],
    });
    const w = mountBoard(); await flushPromises();
    const txt = w.find('.gacha-hl').text();
    expect(w.findAll('.hl-pity').length).toBe(2);   // special + open beginner_choice; spent beginner has none
    expect(txt).toContain('Novice Choice');          // open one-shot DOES show its pity now
    expect(txt).toContain('Beginner');               // spent one-shot still shows its 5★ record (no pity row)
  });

  it('keeps an open 歪 char_exchange (新旅) pity until the FEATURED win (off does not close it)', async () => {
    getSummary.mockResolvedValue({
      ...base,
      perBanner: { char_exchange: 50 },
      highlights: [
        // 歪'd to a standard char (off:true) — the 50/50 pool stays OPEN until the featured win
        { name: 'Std', itemType: 'char', bannerKey: 'char_exchange', time: 't', count: 50, rank: 5, off: true, limited: true },
      ],
      pity: [
        { key: 'char_exchange', label: { en: 'New Journey' }, current: 20, cap: 80, nearPity: false },
      ],
    });
    const w = mountBoard(); await flushPromises();
    expect(w.findAll('.hl-pity').length).toBe(1); // off (歪) is NOT the closing win → pity still shows
    expect(w.find('.gacha-hl').text()).toContain('New Journey');
  });

  it('renders a pity row in the weapon column too', async () => {
    getSummary.mockResolvedValue({
      ...base,
      perBanner: { weapon: 5 },
      highlights: [],
      pity: [{ key: 'weapon', label: { en: 'Weapon' }, current: 12, cap: 80, nearPity: false }],
    });
    const w = mountBoard(); await flushPromises();
    const cols = w.findAll('.hl-col');
    expect(cols[1].find('.hl-pity').exists()).toBe(true);  // weapon column gets the pity row
    expect(cols[0].find('.hl-pity').exists()).toBe(false); // not the character column
    expect(cols[1].text()).toContain('Weapon');
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

  it('renders a 歪 chip on pulls that lost the 50/50', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    const cols = w.findAll('.hl-col');
    // both 5★ entries (Alpha char + Blade weapon) are off:true → one chip per column
    expect(cols[0].findAll('.hl-off').length).toBe(1); // character column
    expect(cols[1].findAll('.hl-off').length).toBe(1); // weapon column
    const offChips = w.findAll('.hl-off');
    expect(offChips.length).toBe(2);
    expect(offChips.every((c) => c.text() === 'Off')).toBe(true); // en locale
  });

  it('has the gacha keys non-empty in every locale (i18n parity)', async () => {
    const en = (await import('../../locales/en.json')).default as Record<string, any>;
    const tw = (await import('../../locales/zh-TW.json')).default as Record<string, any>;
    const cn = (await import('../../locales/zh-CN.json')).default as Record<string, any>;
    for (const loc of [en, tw, cn]) {
      for (const k of ['play_first', 'wrong_account', 'url_expired', 'off',
        'lim_char_cnt', 'lim_weapon_cnt', 'hit_rate', 'avg_char', 'avg_weapon', 'hit_rate_sub']) {
        expect(((loc.gacha?.[k] ?? '') as string).length).toBeGreaterThan(0);
      }
      for (const k of ['title', 'login', 'step1', 'step2', 'step3', 'paste', 'pasteLabel', 'rearm']) {
        expect(((loc.gacha?.link?.[k] ?? '') as string).length).toBeGreaterThan(0);
      }
      expect(((loc.gacha?.currency?.endfield_oroberyl ?? '') as string).length).toBeGreaterThan(0);
    }
  });

  it('groups high-star records by pool with a sub-header per banner', async () => {
    getSummary.mockResolvedValue({
      ...base,
      pity: [
        { key: 'character', label: { en: 'Featured' }, current: 0, cap: 90, nearPity: false },
        { key: 'collab', label: { en: 'Collab' }, current: 0, cap: 90, nearPity: false },
      ],
      perBanner: { character: 5, collab: 5 },
      highlights: [
        { name: 'Lingyang', itemType: '', bannerKey: 'collab', time: 't1', count: 1, rank: 5, off: true },
        { name: 'Jinhsi', itemType: '', bannerKey: 'character', time: 't2', count: 1, rank: 5, off: false },
      ],
    });
    const w = mountBoard(); await flushPromises();
    const heads = w.findAll('.hl-pool .panel-title').map((n) => n.text());
    expect(heads.some((t) => t.includes('Featured'))).toBe(true);
    expect(heads.some((t) => t.includes('Collab'))).toBe(true);
    expect(w.findAll('.hl-off').length).toBe(1); // the 歪 chip is under the collab group
  });
});
