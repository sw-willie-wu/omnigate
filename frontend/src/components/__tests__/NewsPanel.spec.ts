import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const getNewsMock = vi.fn();
const openMock = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetNews: (...a: any[]) => getNewsMock(...a),
  OpenExternalURL: (...a: any[]) => openMock(...a),
}));

import NewsPanel from '../NewsPanel.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });

function mountPanel() {
  const pinia = createPinia();
  setActivePinia(pinia);
  return mount(NewsPanel, {
    props: { gid: 'hoyoverse/genshin' },
    global: { plugins: [pinia, i18n] },
  });
}

describe('NewsPanel', () => {
  beforeEach(() => {
    getNewsMock.mockReset();
    openMock.mockReset();
  });

  it('shows empty state when no items', async () => {
    getNewsMock.mockResolvedValue([]);
    const w = mountPanel();
    await flushPromises();
    expect(w.text()).toContain('No news available');
  });

  it('shows error state on failure', async () => {
    getNewsMock.mockRejectedValue(new Error('boom'));
    const w = mountPanel();
    await flushPromises();
    expect(w.text()).toContain('Failed to load news');
  });

  it('renders items and filters by category', async () => {
    getNewsMock.mockResolvedValue([
      { title: 'Ann1', category: 'announce', date: '2026-01-01', url: 'https://x/1' },
      { title: 'Act1', category: 'activity', date: '2026-01-02', url: 'https://x/2' },
      { title: 'Info1', category: 'info', date: '2026-01-03', url: 'https://x/3' },
    ]);
    const w = mountPanel();
    await flushPromises();
    expect(w.text()).toContain('Ann1');
    expect(w.text()).toContain('Info1'); // "all" shows info
    // click 公告 filter → only announce
    const annBtn = w.findAll('button').find((b) => b.text() === 'Notice');
    expect(annBtn).toBeTruthy();
    await annBtn!.trigger('click');
    expect(w.text()).toContain('Ann1');
    expect(w.text()).not.toContain('Act1');
    expect(w.text()).not.toContain('Info1');
    // 資訊 filter pill shows only info items (only filter pills are <button>s)
    const infoBtn = w.findAll('button').find((b) => b.text() === 'Info');
    expect(infoBtn).toBeTruthy();
    await infoBtn!.trigger('click');
    expect(w.text()).toContain('Info1');
    expect(w.text()).not.toContain('Ann1');
    expect(w.text()).not.toContain('Act1');
  });
});
