import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import BottomBar from '../components/BottomBar.vue';
import en from '../locales/en.json';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';

vi.mock('../composables/useDialog', () => ({ confirm: vi.fn().mockResolvedValue(true) }));
vi.mock('../../wailsjs/go/app/App', () => ({
  Launch: vi.fn(),
  StartUpdate: vi.fn(),
  StartPredownload: vi.fn(),
  CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(),
  RemovePredownload: vi.fn(),
  DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(),
  UpdateStatusAll: vi.fn(async () => ({})),
  CheckForUpdate: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

// A Hypergryph game snapshot with an available update but NO predownload.
// Hypergryph (Endfield) never sets available_predl and does not implement
// the predlExposer interface, so has_predownload is always false.
function hypergryphSnapshot() {
  return {
    available_update: { version: '1.2.6' },
    available_predl: null, // hypergryph never sets this
    predl_ready: null,
    in_flight: null,
    last_error: null,
  };
}

describe('BottomBar predl suppression for Hypergryph', () => {
  it('does not render the predownload button when available_predl is null', async () => {
    setActivePinia(createPinia());
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });

    const wrapper = mount(BottomBar, {
      global: { plugins: [i18n] },
    });

    // Seed a Hypergryph/Endfield game (installed, has_predownload = false)
    const games = useGamesStore();
    games.games = [
      {
        id: 'hypergryph/endfield',
        backend: 'hypergryph',
        display_name: { en: 'Arknights: Endfield' },
        installed: true,
        has_predownload: false,
        current_version: '1.2.0',
        latest_version: '1.2.6',
      },
    ];
    games.selectedID = 'hypergryph/endfield';

    // Seed the updates snapshot: update available, but NO predl
    const updates = useUpdatesStore();
    updates.byGame['hypergryph/endfield'] = hypergryphSnapshot() as any;

    await wrapper.vm.$nextTick();

    // The predl button must NOT be rendered — Hypergryph defers predownload
    expect(wrapper.find('[data-testid="predl-button"]').exists()).toBe(false);
  });
});
