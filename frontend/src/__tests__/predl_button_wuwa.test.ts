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
  Launch: vi.fn(), StartUpdate: vi.fn(), StartPredownload: vi.fn(), CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(), RemovePredownload: vi.fn(), DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(async () => ({})), CheckForUpdate: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

function mountBar() {
  setActivePinia(createPinia());
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(BottomBar, { global: { plugins: [i18n] } });
}

function seedWuwa(snap: any) {
  const games = useGamesStore();
  games.games = [{
    id: 'kurogames/wutheringwaves', backend: 'kurogames',
    display_name: { en: 'Wuthering Waves' }, installed: true,
    has_predownload: true, current_version: '3.3.0', latest_version: '3.3.0',
  } as any];
  games.selectedID = 'kurogames/wutheringwaves';
  const updates = useUpdatesStore();
  updates.byGame['kurogames/wutheringwaves'] = snap;
}

describe('BottomBar predl button (WuWa)', () => {
  it('renders the predl button and info pill when available_predl is set', async () => {
    const wrapper = mountBar();
    seedWuwa({ available_update: null, available_predl: { version: '3.4.0', total_bytes: 1234 }, predl_ready: null, in_flight: null, last_error: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[data-testid="predl-button"]').exists()).toBe(true);
    expect(wrapper.find('.pill').classes()).toContain('info');
  });

  it('keeps the Play button available during predownload (play current while staging next)', async () => {
    const wrapper = mountBar();
    seedWuwa({
      available_update: null, available_predl: null, predl_ready: null,
      in_flight: { kind: 'predownload', phase: 'download', current: 50, total: 100, stage: '' },
      last_error: null,
    });
    await wrapper.vm.$nextTick();
    // predl progress is shown in the predl-area...
    expect(wrapper.find('.predl-area .progress-btn.predl').exists()).toBe(true);
    // ...and the Play button remains available in the launch-area.
    const launchBtn = wrapper.find('.launch-area .launch-btn');
    expect(launchBtn.exists()).toBe(true);
    expect(launchBtn.find('.play-tri').exists()).toBe(true);
  });

  it('does NOT light predl from has_predownload alone (bypass removed)', async () => {
    const wrapper = mountBar();
    seedWuwa({ available_update: null, available_predl: null, predl_ready: null, in_flight: null, last_error: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[data-testid="predl-button"]').exists()).toBe(false);
    const pill = wrapper.find('.pill');
    expect(pill.classes()).toContain('ok');   // ready/green
    expect(pill.classes()).not.toContain('info');
  });
});
