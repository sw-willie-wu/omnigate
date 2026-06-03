import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import NavStrip from '../components/NavStrip.vue';
import en from '../locales/en.json';
import { useViewStore } from '../stores/view';

function mountNav() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(NavStrip, { global: { plugins: [i18n] } });
}

describe('NavStrip', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('marks Overview active and Gacha disabled', () => {
    const w = mountNav();
    const active = w.find('.nav-tab.active');
    expect(active.exists()).toBe(true);
    expect(active.text()).toContain('Overview');

    const disabled = w.find('.nav-tab.disabled');
    expect(disabled.exists()).toBe(true);
    expect(disabled.text()).toContain('Gacha');
  });

  it('gacha tab is natively disabled (inert)', () => {
    const w = mountNav();
    const gacha = w.find('.nav-tab.disabled');
    // native disabled attribute → non-clickable, non-focusable
    expect(gacha.attributes('disabled')).toBeDefined();
  });

  it('overview tab click is wired and sets homeTab back to overview', async () => {
    const w = mountNav();
    const view = useViewStore();
    view.setHomeTab('gacha');                 // move off the default
    await w.find('.nav-tab:not(.disabled)').trigger('click');
    expect(view.homeTab).toBe('overview');    // overview tab's @click fired
  });
});
