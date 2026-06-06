import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { beforeEach, it, expect } from 'vitest';
import BgLayer from '../components/BgLayer.vue';
import { useGamesStore } from '../stores/games';

beforeEach(() => setActivePinia(createPinia()));

function fireCanPlay(wrapper: any) {
  wrapper.findAll('video').forEach((v: any) => v.element.dispatchEvent(new Event('canplay')));
}

it('switching to an image-only background hides both videos', async () => {
  const store = useGamesStore();
  store.games = [{ id: 'g/1', backend: 'b', display_name: { en: 'x' }, installed: true, has_predownload: false,
    backgrounds: [{ image: 'i0', video: 'v0' }, { image: 'i1', video: '' }], bgIndex: 0 } as any];
  store.selectedID = 'g/1';
  const wrapper = mount(BgLayer);
  await wrapper.vm.$nextTick();
  fireCanPlay(wrapper);
  store.games[0].bgIndex = 1;
  await wrapper.vm.$nextTick();
  const vids = wrapper.findAll('video');
  expect(vids.every((v: any) => v.classes().includes('fading'))).toBe(true);
});

it('rapid video1→video2→video1 ends visible on video1 and ignores stale canplay', async () => {
  const store = useGamesStore();
  store.games = [{ id: 'g/1', backend: 'b', display_name: { en: 'x' }, installed: true, has_predownload: false,
    backgrounds: [{ image: 'i0', video: 'v0' }, { image: 'i1', video: 'v1' }], bgIndex: 0 } as any];
  store.selectedID = 'g/1';
  const wrapper = mount(BgLayer);
  await wrapper.vm.$nextTick();
  fireCanPlay(wrapper);
  store.games[0].bgIndex = 1; await wrapper.vm.$nextTick();
  store.games[0].bgIndex = 0; await wrapper.vm.$nextTick();
  fireCanPlay(wrapper);
  const vids = wrapper.findAll('video');
  const visible = vids.filter((v: any) => !v.classes().includes('fading'));
  expect(visible.length).toBe(1);
  expect((visible[0].element as HTMLVideoElement).getAttribute('src')).toBe('v0');
});
