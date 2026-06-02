import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import Sidebar from '../components/Sidebar.vue';
import en from '../locales/en.json';
import { useGamesStore } from '../stores/games';
import { useBackendsStore } from '../stores/backends';

const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });

describe('Sidebar per-backend status dot', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('renders a status dot carrying the backend status class + localized title', async () => {
    const w = mount(Sidebar, { global: { plugins: [i18n] } });
    useGamesStore().games = [
      { id: 'kurogames/wuwa', backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false },
    ] as any;
    useBackendsStore().backends = [
      { backend_id: 'kurogames', display_name: { en: 'Kuro' }, status: 'launcher_missing' },
    ] as any;
    await w.vm.$nextTick();

    const dot = w.find('.group-status-dot');
    expect(dot.exists()).toBe(true);
    expect(dot.classes()).toContain('launcher_missing');
    expect(dot.attributes('title')).toBe('Launcher not found at path');
  });

  it('omits the dot when the backend status is unknown', async () => {
    const w = mount(Sidebar, { global: { plugins: [i18n] } });
    useGamesStore().games = [
      { id: 'kurogames/wuwa', backend: 'kurogames', display_name: { en: 'WuWa' }, installed: true, has_predownload: false },
    ] as any;
    await w.vm.$nextTick();
    expect(w.find('.group-status-dot').exists()).toBe(false);
  });
});
