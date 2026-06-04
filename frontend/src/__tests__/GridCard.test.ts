import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import GridCard from '../components/GridCard.vue';
import en from '../locales/en.json';
import { Launch } from '../../wailsjs/go/app/App';

vi.mock('../../wailsjs/go/app/App', () => ({ Launch: vi.fn() }));

const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });

function mk(row: any) {
  return mount(GridCard, { props: { row }, global: { plugins: [i18n] } });
}

describe('GridCard play button', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('calls Launch with the row id when the play button is clicked', async () => {
    const w = mk({
      id: 'kurogames/wuwa', backend: 'kurogames',
      display_name: { en: 'Wuthering Waves' }, installed: true,
      has_predownload: false, current_version: '3.3.0',
    });
    await w.find('.grid-card-btn').trigger('click');
    expect(Launch).toHaveBeenCalledWith('kurogames/wuwa');
  });

  it('disables the play button when the game is not installed', () => {
    const w = mk({
      id: 'kurogames/wuwa', backend: 'kurogames',
      display_name: { en: 'Wuthering Waves' }, installed: false,
      has_predownload: false,
    });
    expect(w.find('.grid-card-btn').attributes('disabled')).toBeDefined();
  });
});
