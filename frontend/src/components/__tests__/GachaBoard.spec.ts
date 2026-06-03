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

import GachaBoard from '../GachaBoard.vue';

function mountBoard() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(GachaBoard, { props: { gid: 'hypergryph/endfield' }, global: { plugins: [i18n] } });
}
const base = { supported: true, uid: 'u1', totalPulls: 12, perBanner: {}, spendEst: 1200, currency: 'NT$',
  headlineCnt: 2, headlineByType: {}, avgPity: 6, luckScore: 80, winRate5050: null, worstPull: 9,
  pity: [{ key: 'special', label: { en: 'Limited' }, current: 10, cap: 80, nearPity: false }],
  distribution: [1,0,0,0,0,0,0,0,1], recentHeadline: [{ name: 'Alpha', itemType: 'char', bannerKey: 'special', time: 't', count: 4 }] };

describe('GachaBoard', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); });

  it('renders stat cards from summary', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    expect(w.text()).toContain('12');
    expect(w.text()).toContain('Alpha');
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

  it('shows url-reopen guidance when refresh errKind=url', async () => {
    getSummary.mockResolvedValue(base);
    refreshGacha.mockRejectedValue(new Error('gacha history url unavailable'));
    const w = mountBoard(); await flushPromises();
    await w.find('.gacha-refresh').trigger('click'); await flushPromises();
    expect(w.find('.gacha-url-hint').exists()).toBe(true);
  });
});
