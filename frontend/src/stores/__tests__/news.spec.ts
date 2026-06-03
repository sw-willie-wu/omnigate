import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const getNewsMock = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetNews: (...args: any[]) => getNewsMock(...args),
}));

import { useNewsStore } from '../news';

describe('news store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getNewsMock.mockReset();
  });

  it('lazily loads once per gid and caches', async () => {
    getNewsMock.mockResolvedValue([{ title: 'A', category: 'announce', date: '2026-01-01', url: 'https://x/1' }]);
    const s = useNewsStore();
    await s.load('hoyoverse/genshin');
    await s.load('hoyoverse/genshin'); // cached → no second call
    expect(getNewsMock).toHaveBeenCalledTimes(1);
    expect(s.itemsFor('hoyoverse/genshin')).toHaveLength(1);
    expect(s.stateFor('hoyoverse/genshin').loading).toBe(false);
    expect(s.stateFor('hoyoverse/genshin').error).toBe(false);
  });

  it('sets error state on failure', async () => {
    getNewsMock.mockRejectedValue(new Error('boom'));
    const s = useNewsStore();
    await s.load('kurogames/wutheringwaves');
    expect(s.stateFor('kurogames/wutheringwaves').error).toBe(true);
    expect(s.itemsFor('kurogames/wutheringwaves')).toHaveLength(0);
  });

  it('reset() clears cache so next load refetches', async () => {
    getNewsMock.mockResolvedValue([]);
    const s = useNewsStore();
    await s.load('hypergryph/endfield');
    s.reset();
    await s.load('hypergryph/endfield');
    expect(getNewsMock).toHaveBeenCalledTimes(2);
  });
});
