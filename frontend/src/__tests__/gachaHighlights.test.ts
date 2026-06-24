import { describe, it, expect } from 'vitest';
import { isEquip, equipTypeKey, splitByType, distinctRanks, groupByBanner, capGroups, shouldShowPity, isOneShotPool, buildPoolSections, computeCardMetrics, compactNum, baseKey, buildBannerPanels } from '../utils/gachaHighlights';
import type { PoolSection } from '../utils/gachaHighlights';
import type { HeadlineEntry, BannerPity } from '../stores/gacha';

const mk = (bannerKey: string, rank: number, name = 'x', extra: Partial<HeadlineEntry> = {}): HeadlineEntry => ({
  name, itemType: '', bannerKey, time: '', count: 1, rank, off: false, limited: false, ...extra,
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

describe('computeCardMetrics', () => {
  // top rank = 5. featured = limited && !isOneShotPool.
  const hl = [
    mk('character', 5, 'a', { limited: true, count: 60 }),       // featured char
    mk('collab', 5, 'b', { limited: true, count: 40 }),          // featured char (collab)
    mk('weapon', 5, 'c', { limited: true, count: 50 }),          // featured weapon
    mk('standard_char', 5, 'd', { limited: false, count: 10 }),  // standard → not featured, in avgAll
    mk('char_exchange', 5, 'e', { limited: true, count: 1 }),    // limited BUT one-shot → not featured, excl from avgAll
    mk('other', 5, 'f', { limited: false, count: 1 }),           // one-shot gift → excl from avgAll
    mk('character', 4, 'g', { limited: true, count: 5 }),        // second rank → ignored
  ];

  it('counts featured char/weapon top-rank pulls, excluding standard and one-shot', () => {
    const m = computeCardMetrics(hl, 5);
    expect(m.limCharCnt).toBe(2);    // a + b
    expect(m.limWeaponCnt).toBe(1);  // c
  });

  it('averages avgAll over all top-rank pulls except one-shot pools', () => {
    const m = computeCardMetrics(hl, 5);
    // a60 b40 c50 d10 → (60+40+50+10)/4 = 40; e(char_exchange) + f(other) excluded; g is rank 4
    expect(m.avgAll).toBe(40);
  });

  it('averages featured char and featured weapon separately', () => {
    const m = computeCardMetrics(hl, 5);
    expect(m.avgChar).toBe(50);   // (60+40)/2
    expect(m.avgWeapon).toBe(50); // 50/1
  });

  it('rolls a lost 50/50 (off) into the next featured win cost; counts only wins', () => {
    // highlights are newest-first: win A (newer) at index 0, the 歪 (older) at index 1.
    // chronologically: 歪(75 pulls, standard char) then win A(80 pulls) → A cost = 155, 1 char.
    const m = computeCardMetrics([
      mk('character', 5, 'winA', { limited: true, off: false, count: 80 }),
      mk('character', 5, 'lost', { limited: true, off: true, count: 75 }),
    ], 5);
    expect(m.limCharCnt).toBe(1);   // only the featured win is a 限定角色 (the 歪 standard char isn't)
    expect(m.avgChar).toBe(155);    // 75 (小保/歪) + 80 (大保底 win)
    expect(m.hitTotal).toBe(2);     // rate still sees 2 featured rolls
    expect(m.hitWins).toBe(1);
    expect(m.hitRate).toBeCloseTo(0.5, 5);
  });

  it('scopes per-banner pending to the composite key — 歪 in 期A does NOT roll into 期B win cost', () => {
    // highlights newest-first: Fist wins on 期B (newer), Standard 歪s on 期A (older).
    // pending['special:A'] should accumulate 70, but pending['special:B'] starts fresh at 0.
    // The 期B win's acquisition cost must be 40 (its own count only), NOT 70+40=110.
    const m = computeCardMetrics([
      mk('special:B', 5, 'Fist', { limited: true, off: false, count: 40 }),
      mk('special:A', 5, 'Standard', { limited: true, off: true, count: 70 }),
    ], 5);
    expect(m.limCharCnt).toBe(1);     // only the featured win counts as 限定角色
    expect(m.avgChar).toBe(40);       // 期B win cost = 40 alone, NOT 70+40 = 110
    expect(m.hitTotal).toBe(2);       // both featured rolls (歪 + win) counted for the rate
    expect(m.hitWins).toBe(1);
  });

  it('discards a trailing off (in-progress 50/50 loss) from the acquisition average', () => {
    // newest-first: trailing 歪(70) at index 0, win(60) at 1, earlier 歪(50) at 2.
    // chronological: 歪50 → win60 (cost 110, 1 char) → 歪70 (no later win, discarded).
    const m = computeCardMetrics([
      mk('character', 5, 'pending', { limited: true, off: true, count: 70 }),
      mk('character', 5, 'win', { limited: true, off: false, count: 60 }),
      mk('character', 5, 'lost', { limited: true, off: true, count: 50 }),
    ], 5);
    expect(m.limCharCnt).toBe(1);
    expect(m.avgChar).toBe(110);  // 50 + 60; the trailing 70 is excluded
    expect(m.hitTotal).toBe(3);   // rate counts all three featured rolls
    expect(m.hitWins).toBe(1);
  });

  it('computes the featured hit rate over char+weapon, off counting as a loss', () => {
    const m = computeCardMetrics([
      mk('character', 5, 'a', { limited: true, off: false }),
      mk('character', 5, 'b', { limited: true, off: true }),   // 歪
      mk('weapon', 5, 'c', { limited: true, off: false }),
      mk('standard_char', 5, 'd', { limited: false, off: false }), // excluded from rate
    ], 5);
    expect(m.hitTotal).toBe(3);
    expect(m.hitWins).toBe(2);
    expect(m.hitRate).toBeCloseTo(2 / 3, 5);
  });

  it('returns null averages/rate on empty or zero-denominator input', () => {
    const m = computeCardMetrics([], 0);
    expect(m.avgAll).toBeNull();
    expect(m.avgChar).toBeNull();
    expect(m.avgWeapon).toBeNull();
    expect(m.hitRate).toBeNull();
    expect(m.limCharCnt).toBe(0);
    expect(m.distribution).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0]);
  });

  it('builds a 9-bucket distribution over the same population as avgAll (excl one-shot)', () => {
    const m = computeCardMetrics([
      mk('character', 5, 'a', { count: 5 }),        // bucket 0 (1-9)
      mk('standard_char', 5, 'b', { count: 72 }),   // bucket 7 (70-79) — standard is non-one-shot → in
      mk('weapon', 5, 'c', { count: 80 }),          // bucket 8 (80+)
      mk('other', 5, 'd', { count: 1 }),            // one-shot gift → excluded
      mk('character', 4, 'e', { count: 30 }),       // not top rank → excluded
    ], 5);
    expect(m.distribution.length).toBe(9);
    expect(m.distribution[0]).toBe(1);  // a only ('other' excluded despite also being count 1)
    expect(m.distribution[7]).toBe(1);  // b
    expect(m.distribution[8]).toBe(1);  // c (>=80)
    expect(m.distribution.reduce((s, v) => s + v, 0)).toBe(3); // a,b,c; not d (one-shot) or e (rank 4)
  });
});

describe('compactNum', () => {
  it('writes values below 10k in full', () => {
    expect(compactNum(1200)).toBe((1200).toLocaleString());
    expect(compactNum(9999)).toBe((9999).toLocaleString());
    expect(compactNum(0)).toBe('0');
  });
  it('abbreviates thousands as K (1 dp under 100K, integer at/above)', () => {
    expect(compactNum(12800)).toBe('12.8K');
    expect(compactNum(420640)).toBe('421K');
  });
  it('abbreviates millions as M', () => {
    expect(compactNum(1200000)).toBe('1.2M');
  });
  it('rolls a K value that would round to 1000K up to M', () => {
    expect(compactNum(999520)).toBe('1.0M'); // not "1000K"
    expect(compactNum(999360)).toBe('999K');  // still K just below the rollover
  });
});

describe('baseKey + composite-aware classification', () => {
  it('baseKey strips the :poolId suffix', () => {
    expect(baseKey('weapon:weponbox_1_1_2')).toBe('weapon');
    expect(baseKey('special:special_1_3_1')).toBe('special');
    expect(baseKey('standard')).toBe('standard');
  });
  it('isEquip works on composite weapon keys', () => {
    expect(isEquip('weapon:weponbox_1_1_2')).toBe(true);
    expect(isEquip('special:special_1_3_1')).toBe(false);
  });
  it('equipTypeKey works on composite keys', () => {
    expect(equipTypeKey('weapon:weponbox_1_1_2')).toBe('weapon');
  });
  it('isOneShotPool works on composite keys', () => {
    expect(isOneShotPool('special:special_1_3_1')).toBe(false);
    expect(isOneShotPool('beginner')).toBe(true);
  });
  it('splitByType routes composite weapon keys to weapons column', () => {
    const r = splitByType([
      { name: 'WX', itemType: 'weapon', bannerKey: 'weapon:weponbox_1_1_2', time: '', count: 1, rank: 6, off: false, limited: true, icon: '' },
      { name: 'Lim', itemType: 'char', bannerKey: 'special:special_1_3_1', time: '', count: 1, rank: 6, off: false, limited: true, icon: '' },
    ]);
    expect(r.weapons.map((h) => h.name)).toEqual(['WX']);
    expect(r.chars.map((h) => h.name)).toEqual(['Lim']);
  });
});

describe('buildBannerPanels', () => {
  const pity = (key: string, label: Record<string, string>, current = 0, cap = 80): BannerPity => ({ key, label, current, cap, nearPity: false });
  const sec = (key: string, label: Record<string, string>, p: BannerPity | null): PoolSection => ({ key, label, pity: p, entries: [], total: 0 });

  it('groups composite subs under one panel with the bare-key topPity', () => {
    const topByBase = new Map<string, BannerPity>([['special', pity('special', { 'zh-TW': '特許尋訪' }, 5)]]);
    const sections = [
      sec('special:B', { 'zh-TW': '特許尋訪 - 期B' }, pity('special:B', { 'zh-TW': '特許尋訪 - 期B' }, 4)),
      sec('special:A', { 'zh-TW': '特許尋訪 - 期A' }, pity('special:A', { 'zh-TW': '特許尋訪 - 期A' }, 1)),
    ];
    const panels = buildBannerPanels(sections, topByBase);
    expect(panels).toHaveLength(1);
    expect(panels[0].base).toBe('special');
    expect(panels[0].label['zh-TW']).toBe('特許尋訪');
    expect(panels[0].topPity?.current).toBe(5);
    expect(panels[0].subs.map((s) => s.label['zh-TW'])).toEqual(['期B', '期A']);
  });

  it('weapon panel has no topPity (base absent from topByBase)', () => {
    const sections = [
      sec('weapon:X', { 'zh-TW': '緋珀申領' }, pity('weapon:X', { 'zh-TW': '緋珀申領' }, 0, 40)),
      sec('weapon:Y', { 'zh-TW': '行舟申領' }, pity('weapon:Y', { 'zh-TW': '行舟申領' }, 1, 40)),
    ];
    const panels = buildBannerPanels(sections, new Map());
    expect(panels).toHaveLength(1);
    expect(panels[0].base).toBe('weapon');
    expect(panels[0].topPity).toBeNull();
    expect(panels[0].subs).toHaveLength(2);
  });

  it('non-PerPool bare banner is a single-sub panel, no topPity', () => {
    const sections = [sec('standard', { 'zh-TW': '基礎尋訪' }, pity('standard', { 'zh-TW': '基礎尋訪' }, 7))];
    const panels = buildBannerPanels(sections, new Map());
    expect(panels).toHaveLength(1);
    expect(panels[0].topPity).toBeNull();
    expect(panels[0].subs).toHaveLength(1);
    expect(panels[0].label['zh-TW']).toBe('基礎尋訪');
    expect(panels[0].subs[0].label['zh-TW']).toBe('基礎尋訪');
  });
});
