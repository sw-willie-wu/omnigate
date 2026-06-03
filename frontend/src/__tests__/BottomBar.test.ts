import { describe, it, expect, test, beforeEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import BottomBar from '../components/BottomBar.vue';
import en from '../locales/en.json';
import zhTW from '../locales/zh-TW.json';
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

  test('cancel button is rendered as [disabled] (not hidden) during applying stage', async () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'zh-TW',
      messages: { 'zh-TW': zhTW },
    });

    const wrapper = mount(BottomBar, {
      global: { plugins: [i18n] },
    });

    // Set up a selected game in the games store
    const games = useGamesStore();
    games.games = [
      {
        id: 'hoyoverse/genshin',
        backend: 'hoyoverse',
        display_name: { en: 'Genshin Impact' },
        installed: true,
        has_predownload: false,
        current_version: '5.6.0',
        latest_version: '5.7.0',
      },
    ];
    games.selectedID = 'hoyoverse/genshin';

    // Set in_flight to apply phase with ETA
    const updates = useUpdatesStore();
    updates.byGame['hoyoverse/genshin'] = {
      in_flight: {
        kind: 'update',
        phase: 'apply',
        stage: 'applying',
        estimated_seconds_remaining: 60,
        current: 5,
        total: 10,
        version: '5.7.0',
        started_at: new Date().toISOString(),
      },
    } as any;

    await wrapper.vm.$nextTick();

    const cancelDisabled = wrapper.find('.cancel-x.disabled');
    expect(cancelDisabled.exists()).toBe(true);
    // The tooltip should contain the ETA text (1 minute ceiling of 60s)
    const title = cancelDisabled.attributes('title') ?? '';
    expect(title).toContain('1');
  });

  test('renders generic last_error.code via update.error.<code> (sophon_no_install)', async () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en },
    });

    const wrapper = mount(BottomBar, {
      global: { plugins: [i18n] },
    });

    const games = useGamesStore();
    games.games = [
      {
        id: 'hoyoverse/genshin',
        backend: 'hoyoverse',
        display_name: { en: 'Genshin Impact' },
        installed: true,
        has_predownload: false,
        current_version: '',
        latest_version: '6.6.0',
      },
    ];
    games.selectedID = 'hoyoverse/genshin';

    const updates = useUpdatesStore();
    updates.byGame['hoyoverse/genshin'] = {
      last_error: { code: 'sophon_no_install', retryable: false },
    } as any;

    await wrapper.vm.$nextTick();

    const errEl = wrapper.find('.update-error');
    expect(errEl.exists()).toBe(true);
    expect(errEl.text()).toContain('Use HoYoPlay for initial install');
  });

  // Full 8-row table-driven test deferred to follow-up M3.A.v2 pass.

  test('meta line shows never_played when last_played is unset', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
    const wrapper = mount(BottomBar, { global: { plugins: [i18n] } });
    const games = useGamesStore();
    games.games = [{ id: 'fake/g', backend: 'fake', display_name: { en: 'G' }, installed: true, has_predownload: false, current_version: '1.0' }];
    games.selectedID = 'fake/g';
    await wrapper.vm.$nextTick();
    expect(wrapper.find('.last-played').text()).toContain('Never played');
  });

  test('meta line shows last-played when set', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
    const wrapper = mount(BottomBar, { global: { plugins: [i18n] } });
    const games = useGamesStore();
    games.games = [{ id: 'fake/g', backend: 'fake', display_name: { en: 'G' }, installed: true, has_predownload: false, current_version: '1.0', last_played: new Date().toISOString() }];
    games.selectedID = 'fake/g';
    await wrapper.vm.$nextTick();
    const txt = wrapper.find('.last-played').text();
    expect(txt).toContain('Last played');
    expect(txt).toContain('Today');
  });
});
