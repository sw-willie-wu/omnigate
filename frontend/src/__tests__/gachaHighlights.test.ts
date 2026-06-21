import { describe, it, expect } from 'vitest';
import { isEquip, equipTypeKey, splitByType, distinctRanks } from '../utils/gachaHighlights';
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
});
