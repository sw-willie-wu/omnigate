import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import SidebarRow from '../components/SidebarRow.vue';
import en from '../locales/en.json';
import { useUpdatesStore } from '../stores/updates';

vi.mock('../composables/useDialog', () => ({ confirm: vi.fn().mockResolvedValue(true) }));
vi.mock('../../wailsjs/go/app/App', () => ({
  Launch: vi.fn(), StartUpdate: vi.fn(), StartPredownload: vi.fn(), CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(), RemovePredownload: vi.fn(), DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(async () => ({})), CheckForUpdate: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

function mountRow(rowExtra: any, snap: any) {
  setActivePinia(createPinia());
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const row = {
    id: 'kurogames/wutheringwaves', backend: 'kurogames',
    display_name: { en: 'Wuthering Waves' }, installed: true,
    has_predownload: false, current_version: '3.3.0', latest_version: '3.3.0',
    ...rowExtra,
  };
  const wrapper = mount(SidebarRow, { props: { row } as any, global: { plugins: [i18n] } });
  const updates = useUpdatesStore();
  if (snap) updates.byGame[row.id] = snap;
  return wrapper;
}

describe('SidebarRow predl status', () => {
  it('shows the predownload chip when available_predl is set', async () => {
    const wrapper = mountRow({}, { available_predl: { version: '3.4.0' }, in_flight: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('.game-status-mini').classes()).toContain('predownload');
  });

  it('does NOT show predownload from has_predownload alone', async () => {
    const wrapper = mountRow({ has_predownload: true }, { available_predl: null, in_flight: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('.game-status-mini').classes()).not.toContain('predownload');
    expect(wrapper.find('.game-status-mini').classes()).toContain('ready');
  });
});
