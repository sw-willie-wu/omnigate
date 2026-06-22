import { describe, it, expect } from 'vitest';
import { isEquip, equipTypeKey, splitByType, distinctRanks, groupByBanner, capGroups, shouldShowPity, isOneShotPool, buildPoolSections } from '../utils/gachaHighlights';
import type { HeadlineEntry, BannerPity } from '../stores/gacha';

const mk = (bannerKey: string, rank: number, name = 'x'): HeadlineEntry => ({
  name, itemType: '', bannerKey, time: '', count: 1, rank, off: false,
});

describe('gachaHighlights', () => {
  it('classifies equipment banners (weapon/lightcone/wengine) vs characters', () => {
    expect(isEquip('weapon')).toBe(true);
    expect(isEquip('standard_weapon')).toBe(true);
    expect(isEquip('lightcone')).toBe(true);
    expect(isEquip('wengine')).toBe(true);
    expect(isEquip('character')).toBe(false);
    expect(isEquip('standard_char')).toBe(false);
    expect(isEquip('beginner')).toBe(false);
  });

  it('maps a banner key to its equipment item_type label key', () => {
    expect(equipTypeKey('lightcone')).toBe('lightcone');
    expect(equipTypeKey('wengine')).toBe('wengine');
    expect(equipTypeKey('weapon')).toBe('weapon');
    expect(equipTypeKey('standard_weapon')).toBe('weapon');
  });

  it('splits into characters and weapons, preserving order', () => {
    const list = [mk('character', 5, 'a'), mk('weapon', 5, 'b'), mk('lightcone', 4, 'c'), mk('standard_char', 4, 'd')];
    const { chars, weapons } = splitByType(list);
    expect(chars.map((h) => h.name)).toEqual(['a', 'd']);
    expect(weapons.map((h) => h.name)).toEqual(['b', 'c']);
  });

  it('lists distinct ranks highest-first', () => {
    expect(distinctRanks([mk('character', 5), mk('weapon', 4), mk('character', 5), mk('weapon', 6)])).toEqual([6, 5, 4]);
  });

  it('routes the new WuWa weapon pools to the weapon side', () => {
    expect(isEquip('weapon_exchange')).toBe(true);
    expect(isEquip('collab_weapon')).toBe(true);
    expect(isEquip('collab')).toBe(false);       // collab character → char side
    expect(isEquip('char_exchange')).toBe(false);
  });
});

describe('banner grouping', () => {
  const order = [
    { key: 'character', label: { en: 'Featured' } },
    { key: 'standard_char', label: { en: 'Standard' } },
    { key: 'collab', label: { en: 'Collab' } },
  ];
  it('groups by bannerKey in `order`, keeps entry order, drops empty groups', () => {
    const list = [mk('character', 5, 'a'), mk('collab', 5, 'b'), mk('character', 4, 'c')];
    const g = groupByBanner(list, order);
    expect(g.map((x) => x.key)).toEqual(['character', 'collab']); // standard_char empty → absent; order honoured
    expect(g[0].entries.map((e) => e.name)).toEqual(['a', 'c']);
    expect(g[0].total).toBe(2);
  });
  it('puts unknown bannerKeys in a trailing catch-all group (nothing dropped)', () => {
    const list = [mk('character', 5, 'a'), mk('mystery_pool', 5, 'z')];
    const g = groupByBanner(list, order);
    expect(g.map((x) => x.key)).toEqual(['character', 'mystery_pool']);
    expect(g[1].entries.map((e) => e.name)).toEqual(['z']);
  });
  it('capGroups spends a visible budget across groups in order, truncating', () => {
    const groups = [
      { key: 'character', label: {}, entries: [mk('character', 5, 'a'), mk('character', 5, 'b')], total: 2 },
      { key: 'collab', label: {}, entries: [mk('collab', 5, 'c')], total: 1 },
    ];
    const capped = capGroups(groups, 2);
    expect(capped.map((x) => x.key)).toEqual(['character']); // budget 2 fully consumed by group 1
    expect(capped[0].entries.length).toBe(2);
    expect(capped[0].total).toBe(2); // total preserved (un-truncated count)
  });
});

describe('shouldShowPity', () => {
  it('always shows ongoing banners the account pulled on', () => {
    expect(shouldShowPity('character', 10, true)).toBe(true);
    expect(shouldShowPity('character', 10, false)).toBe(true);
    expect(shouldShowPity('collab', 5, true)).toBe(true);
    expect(shouldShowPity('chronicled', 100, true)).toBe(true);
  });
  it('hides any banner with no pulls', () => {
    expect(shouldShowPity('character', 0, false)).toBe(false);
    expect(shouldShowPity('beginner', 0, false)).toBe(false);
  });
  it('hides a one-shot pool that already produced its headline 5★', () => {
    expect(shouldShowPity('beginner', 50, true)).toBe(false);
    expect(shouldShowPity('other', 1, true)).toBe(false);
    expect(shouldShowPity('beginner_choice', 10, true)).toBe(false);
    expect(shouldShowPity('char_exchange', 80, true)).toBe(false);
    expect(shouldShowPity('weapon_exchange', 80, true)).toBe(false);
  });
  it('keeps a one-shot pool still mid-progress (no headline yet)', () => {
    expect(shouldShowPity('beginner', 30, false)).toBe(true);
    expect(shouldShowPity('char_exchange', 40, false)).toBe(true);
  });
});

describe('isOneShotPool', () => {
  it('flags one-shot/finite pools', () => {
    for (const k of ['beginner', 'beginner_choice', 'other', 'char_exchange', 'weapon_exchange']) {
      expect(isOneShotPool(k)).toBe(true);
    }
  });
  it('does not flag ongoing banners', () => {
    for (const k of ['character', 'weapon', 'standard_char', 'standard_weapon', 'collab', 'collab_weapon', 'chronicled', 'lightcone', 'wengine']) {
      expect(isOneShotPool(k)).toBe(false);
    }
  });
});

describe('buildPoolSections', () => {
  const order = [
    { key: 'character', label: { en: 'Featured' } },
    { key: 'standard_char', label: { en: 'Standard' } },
  ];
  const pity = (key: string, current: number, cap = 90): BannerPity =>
    ({ key, label: { en: key }, current, cap, nearPity: false });

  it('merges pity rows and record groups in banner order', () => {
    const s = buildPoolSections(
      [mk('character', 5, 'a'), mk('standard_char', 5, 'b')],
      [pity('character', 7), pity('standard_char', 20)],
      order, 50,
    );
    expect(s.map((x) => x.key)).toEqual(['character', 'standard_char']);
    expect(s[0].pity?.current).toBe(7);
    expect(s[0].entries.map((e) => e.name)).toEqual(['a']);
    expect(s[1].pity?.current).toBe(20);
  });

  it('emits a pity-only section (pity set, no entries, total 0) for a pool with no records', () => {
    const s = buildPoolSections(
      [mk('character', 5, 'a')],
      [pity('character', 7), pity('standard_char', 12)],
      order, 50,
    );
    const std = s.find((x) => x.key === 'standard_char')!;
    expect(std.pity?.current).toBe(12);
    expect(std.entries).toEqual([]);
    expect(std.total).toBe(0);
  });

  it('emits a records-only section (pity null) when a pool has no pity row', () => {
    const s = buildPoolSections([mk('character', 5, 'a')], [], order, 50);
    expect(s).toHaveLength(1);
    expect(s[0].key).toBe('character');
    expect(s[0].pity).toBeNull();
    expect(s[0].entries.map((e) => e.name)).toEqual(['a']);
  });

  it('shares the budget across record sections but never drops a section with a pity row', () => {
    const s = buildPoolSections(
      [mk('character', 5, 'a'), mk('character', 5, 'b'), mk('standard_char', 5, 'c')],
      [pity('standard_char', 30)], // pity only on the second pool
      order, 2, // budget 2 → fully consumed by 'character'
    );
    expect(s.map((x) => x.key)).toEqual(['character', 'standard_char']);
    expect(s[0].entries.length).toBe(2);
    expect(s[1].entries).toEqual([]);    // budget gone
    expect(s[1].pity?.current).toBe(30); // kept anyway — has a pity row
    expect(s[1].total).toBe(1);          // un-truncated record count preserved
  });

  it('omits a record-only section whose entries are fully budget-trimmed', () => {
    expect(buildPoolSections([mk('character', 5, 'a')], [], order, 0)).toEqual([]);
  });

  it('keeps an unmapped banner (catch-all) trailing, with no pity and empty label', () => {
    const s = buildPoolSections(
      [mk('character', 5, 'a'), mk('mystery', 5, 'z')],
      [pity('character', 7)],
      order, 50,
    );
    expect(s.map((x) => x.key)).toEqual(['character', 'mystery']);
    expect(s[1].pity).toBeNull();
    expect(s[1].label).toEqual({});
    expect(s[1].entries.map((e) => e.name)).toEqual(['z']);
  });
});
