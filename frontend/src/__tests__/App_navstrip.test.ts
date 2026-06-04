import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../locales/en.json';
import NavStrip from '../components/NavStrip.vue';
import { useViewStore } from '../stores/view';

describe('home-tab contract', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('view store defaults homeTab to overview', () => {
    expect(useViewStore().homeTab).toBe('overview');
  });

  it('NavStrip overview tab is active by default', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
    const w = mount(NavStrip, { global: { plugins: [i18n] } });
    expect(w.find('.nav-tab.active').text()).toContain('Overview');
  });
});
