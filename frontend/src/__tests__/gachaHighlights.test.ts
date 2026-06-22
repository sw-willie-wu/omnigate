import { describe, it, expect } from 'vitest';
import { isEquip, equipTypeKey, splitByType, distinctRanks, groupByBanner, capGroups } from '../utils/gachaHighlights';
import type { HeadlineEntry } from '../stores/gacha';

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
