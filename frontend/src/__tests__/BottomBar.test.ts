import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import BottomBar from '../components/BottomBar.vue';
import en from '../locales/en.json';

vi.mock('../composables/useDialog', () => ({ confirm: vi.fn().mockResolvedValue(true) }));
vi.mock('../../wailsjs/go/app/App', () => ({ Launch: vi.fn() }));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

describe('BottomBar smoke', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('mounts without error', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en },
    });
    const wrapper = mount(BottomBar, {
      global: {
        plugins: [i18n],
      },
    });
    // Smoke: BottomBar renders nothing when no game selected (v-if="games.selected")
    expect(wrapper.exists()).toBe(true);
  });

  // Full 8-row table-driven test deferred to follow-up M3.A.v2 pass.
});
