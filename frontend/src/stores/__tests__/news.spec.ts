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

  it('lazily loads once per (gid,lang) and caches', async () => {
    getNewsMock.mockResolvedValue([{ title: 'A', category: 'announce', date: '2026-01-01', url: 'https://x/1' }]);
    const s = useNewsStore();
    await s.load('hoyoverse/genshin', 'en');
    await s.load('hoyoverse/genshin', 'en'); // cached → no second call
    expect(getNewsMock).toHaveBeenCalledTimes(1);
    expect(s.itemsFor('hoyoverse/genshin', 'en')).toHaveLength(1);
    expect(s.stateFor('hoyoverse/genshin', 'en').loading).toBe(false);
    expect(s.stateFor('hoyoverse/genshin', 'en').error).toBe(false);
  });

  it('refetches when the language changes (different cache key)', async () => {
    getNewsMock.mockResolvedValue([]);
    const s = useNewsStore();
    await s.load('hoyoverse/genshin', 'en');
    await s.load('hoyoverse/genshin', 'zh-TW'); // different lang → new key → refetch
    expect(getNewsMock).toHaveBeenCalledTimes(2);
    expect(getNewsMock).toHaveBeenNthCalledWith(1, 'hoyoverse/genshin', 'en');
    expect(getNewsMock).toHaveBeenNthCalledWith(2, 'hoyoverse/genshin', 'zh-TW');
  });

  it('sets error state on failure', async () => {
    getNewsMock.mockRejectedValue(new Error('boom'));
    const s = useNewsStore();
    await s.load('kurogames/wutheringwaves', 'en');
    expect(s.stateFor('kurogames/wutheringwaves', 'en').error).toBe(true);
    expect(s.itemsFor('kurogames/wutheringwaves', 'en')).toHaveLength(0);
  });

  it('reset() clears cache so next load refetches', async () => {
    getNewsMock.mockResolvedValue([]);
    const s = useNewsStore();
    await s.load('hypergryph/endfield', 'en');
    s.reset();
    await s.load('hypergryph/endfield', 'en');
    expect(getNewsMock).toHaveBeenCalledTimes(2);
  });
});
