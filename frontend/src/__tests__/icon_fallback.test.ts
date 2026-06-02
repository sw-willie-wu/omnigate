import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import SidebarRow from '../components/SidebarRow.vue';
import en from '../locales/en.json';
import { fallbackIcon } from '../utils/iconFallback';

const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });

function mk(row: any) {
  return mount(SidebarRow, { props: { row }, global: { plugins: [i18n] } });
}

describe('fallbackIcon util', () => {
  it('returns a bundled icon for each of the 5 known games', () => {
    for (const id of [
      'hoyoverse/genshin', 'hoyoverse/starrail', 'hoyoverse/zzz',
      'kurogames/wutheringwaves', 'hypergryph/endfield',
    ]) {
      expect(fallbackIcon(id), id).toBeTruthy();
    }
  });

  it('returns undefined for an unknown game', () => {
    expect(fallbackIcon('nintendo/mario')).toBeUndefined();
  });
});

describe('SidebarRow icon fallback', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('shows the bundled fallback when a known game has no live icon yet', () => {
    const w = mk({
      id: 'kurogames/wutheringwaves', backend: 'kurogames',
      display_name: { en: 'Wuthering Waves' }, installed: true, has_predownload: false,
    });
    const img = w.find('.game-icon img');
    expect(img.exists()).toBe(true);
    expect(img.attributes('src')).toBe(fallbackIcon('kurogames/wutheringwaves'));
  });

  it('swaps to the fallback when the live icon errors', async () => {
    const w = mk({
      id: 'hypergryph/endfield', backend: 'hypergryph',
      display_name: { en: 'Endfield' }, installed: true, has_predownload: false,
      icon_url: '/_asset/hypergryph/icon/endfield',
    });
    const img = w.find('.game-icon img');
    expect(img.attributes('src')).toBe('/_asset/hypergryph/icon/endfield');
    await img.trigger('error');
    expect(img.attributes('src')).toBe(fallbackIcon('hypergryph/endfield'));
  });

  it('falls back to the initial letter for an unknown game with no icon', () => {
    const w = mk({
      id: 'nintendo/mario', backend: 'nintendo',
      display_name: { en: 'Mario' }, installed: true, has_predownload: false,
    });
    expect(w.find('.game-icon img').exists()).toBe(false);
    expect(w.find('.game-icon span').text()).toBe('M');
  });
});
