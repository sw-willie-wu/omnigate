import { describe, it, expect, beforeEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { useViewStore } from '../stores/view';

describe('view store settings drawer', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('starts closed and toggles open/closed', () => {
    const v = useViewStore();
    expect(v.settingsOpen).toBe(false);
    v.openSettings();
    expect(v.settingsOpen).toBe(true);
    v.closeSettings();
    expect(v.settingsOpen).toBe(false);
  });
});
