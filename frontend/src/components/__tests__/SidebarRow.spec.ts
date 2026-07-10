import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(), GetBackgrounds: vi.fn(),
  SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(), RefreshGame: vi.fn(), GetCustomBackground: vi.fn(),
  StartUpdate: vi.fn(), CancelInFlight: vi.fn(), StartPredownload: vi.fn(),
  ApplyPredownload: vi.fn(), RemovePredownload: vi.fn(), CheckForUpdate: vi.fn(),
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

import SidebarRow from '../SidebarRow.vue';
import { useUpdatesStore } from '../../stores/updates';

const GID = 'kurogames/wutheringwaves';
const row = {
  id: GID, backend: 'kurogames', display_name: { en: 'WuWa' },
  installed: true, current_version: '3.4.1', latest_version: '3.5.0',
} as never;

function setup(inFlight: Record<string, unknown> | null) {
  setActivePinia(createPinia());
  const updates = useUpdatesStore();
  if (inFlight) {
    updates.byGame[GID] = { in_flight: inFlight } as never;
  }
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(SidebarRow, { props: { row }, global: { plugins: [i18n] } });
}

describe('SidebarRow progress clamp (T11)', () => {
  it('clamps fill width to 100% when current > total', () => {
    const w = setup({ kind: 'update', phase: 'download', current: 150, total: 100 });
    const fill = w.find('.game-progress-fill');
    expect(fill.exists()).toBe(true);
    expect(fill.attributes('style')).toContain('width: 100%');
  });

  it('renders proportional width under 100%', () => {
    const w = setup({ kind: 'update', phase: 'download', current: 25, total: 100 });
    const fill = w.find('.game-progress-fill');
    expect(fill.exists()).toBe(true);
    expect(fill.attributes('style')).toContain('width: 25%');
  });
});
