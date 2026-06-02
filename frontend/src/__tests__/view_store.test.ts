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

  it('settings is a peer view; toggling off returns to the previous content view', () => {
    const v = useViewStore();
    v.setView('grid');
    v.toggleSettings();
    expect(v.viewMode).toBe('settings');
    expect(v.settingsOpen).toBe(true);
    v.toggleSettings();
    expect(v.viewMode).toBe('grid'); // returned to where we came from
    expect(v.settingsOpen).toBe(false);
  });

  it('switching to a content view while in settings exits settings (no stacking)', () => {
    const v = useViewStore();
    v.openSettings();
    expect(v.viewMode).toBe('settings');
    v.setView('grid');
    expect(v.viewMode).toBe('grid');
    expect(v.settingsOpen).toBe(false);
  });
});
