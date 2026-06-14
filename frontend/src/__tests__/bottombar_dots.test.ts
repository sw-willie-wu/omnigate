import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { beforeEach, it, expect } from 'vitest';
import { createI18n } from 'vue-i18n';
import BottomBar from '../components/BottomBar.vue';
import { useGamesStore } from '../stores/games';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {} }, missingWarn: false, fallbackWarn: false });

beforeEach(() => setActivePinia(createPinia()));

function mountWith(backgrounds: any[], bgIndex: number) {
  const store = useGamesStore();
  store.games = [{ id: 'g/1', backend: 'b', display_name: { en: 'x' }, installed: true, has_predownload: false, backgrounds, bgIndex } as any];
  store.selectedID = 'g/1';
  return mount(BottomBar, { global: { plugins: [i18n], stubs: { GameConfigPopover: true } } });
}

it('renders one dot per background, active on bgIndex', () => {
  const wrapper = mountWith([{ image: 'a', video: '' }, { image: 'b', video: '' }, { image: 'c', video: '' }], 1);
  const dots = wrapper.findAll('[data-test="bg-dot"]');
  expect(dots).toHaveLength(3);
  expect(dots[1].classes()).toContain('active');
});

it('clicking a dot sets bgIndex on the selected row', async () => {
  const wrapper = mountWith([{ image: 'a', video: '' }, { image: 'b', video: '' }], 0);
  await wrapper.findAll('[data-test="bg-dot"]')[1].trigger('click');
  expect(useGamesStore().selected!.bgIndex).toBe(1);
});

it('hides dots when fewer than 2 backgrounds', () => {
  const wrapper = mountWith([{ image: 'a', video: '' }], 0);
  expect(wrapper.findAll('[data-test="bg-dot"]')).toHaveLength(0);
});

it('active dot follows a programmatic bgIndex change (reseed path)', async () => {
  const wrapper = mountWith([{ image: 'a', video: '' }, { image: 'b', video: '' }, { image: 'c', video: '' }], 0);
  useGamesStore().selected!.bgIndex = 2;
  await wrapper.vm.$nextTick();
  expect(wrapper.findAll('[data-test="bg-dot"]')[2].classes()).toContain('active');
});
