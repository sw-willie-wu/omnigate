import { describe, it, expect, beforeEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { useViewStore } from '../stores/view';

// Mirrors App.vue's `<BottomBar v-if="view.viewMode === 'detail' && view.homeTab === 'overview'">`.
// The launch/settings/version bar belongs to the 總覽 tab only, never 抽卡分析.
const bottomBarVisible = (v: ReturnType<typeof useViewStore>) =>
  v.viewMode === 'detail' && v.homeTab === 'overview';

describe('BottomBar visibility guard', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('shows on the overview (總覽) detail tab', () => {
    const v = useViewStore();
    v.setView('detail');
    v.setHomeTab('overview');
    expect(bottomBarVisible(v)).toBe(true);
  });

  it('hides on the gacha (抽卡分析) tab', () => {
    const v = useViewStore();
    v.setView('detail');
    v.setHomeTab('gacha');
    expect(bottomBarVisible(v)).toBe(false);
  });

  it('hides outside detail (settings) regardless of tab', () => {
    const v = useViewStore();
    v.openSettings();
    v.setHomeTab('overview');
    expect(bottomBarVisible(v)).toBe(false);
  });
});
