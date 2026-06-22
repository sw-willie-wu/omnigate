import type { HeadlineEntry } from '../stores/gacha';

// Banner keys whose high-rarity drops are equipment (weapon-slot), not characters.
// Light cones (HSR) and W-engines (ZZZ) are the weapon equivalents → weapon side;
// WuWa's weapon pools (incl. weapon_exchange 武器新旅換取, collab_weapon 武器聯動) too.
const EQUIP_BANNERS = new Set(['weapon', 'standard_weapon', 'weapon_exchange', 'collab_weapon', 'lightcone', 'wengine']);

export function isEquip(bannerKey: string): boolean {
  return EQUIP_BANNERS.has(bannerKey);
}

// The gacha.item_type.* key naming this game's equipment kind, from a banner key
// (lightcone → 光錐, wengine → 音擎, else 武器). Used for the weapon column heading.
export function equipTypeKey(bannerKey: string): 'lightcone' | 'wengine' | 'weapon' {
  if (bannerKey === 'lightcone') return 'lightcone';
  if (bannerKey === 'wengine') return 'wengine';
  return 'weapon';
}

export interface SplitHighlights {
  chars: HeadlineEntry[];
  weapons: HeadlineEntry[];
}

// splitByType partitions highlights into character vs weapon/equipment, preserving
// the input order (already newest-first from the backend).
export function splitByType(list: HeadlineEntry[]): SplitHighlights {
  const chars: HeadlineEntry[] = [];
  const weapons: HeadlineEntry[] = [];
  for (const h of list) (isEquip(h.bannerKey) ? weapons : chars).push(h);
  return { chars, weapons };
}

// distinctRanks returns the rarities present, highest first (e.g. [5,4] or [6,5]),
// so the rank toggle adapts per game.
export function distinctRanks(list: HeadlineEntry[]): number[] {
  return [...new Set(list.map((h) => h.rank))].sort((a, b) => b - a);
}

export interface BannerGroup {
  key: string;
  label: Record<string, string>;
  entries: HeadlineEntry[];
  total: number; // un-truncated count (for the sub-header), preserved through capGroups
}

// groupByBanner partitions one column's highlights (newest-first) into per-banner
// groups, ordered by `order` (the summary `pity[]`, in Banners order). Each group keeps
// input order. Any bannerKey absent from `order` lands in a trailing catch-all group so
// no entry is ever dropped (forward-guard for an unmapped future cardPoolType).
export function groupByBanner(
  list: HeadlineEntry[],
  order: { key: string; label: Record<string, string> }[],
): BannerGroup[] {
  const byKey = new Map<string, HeadlineEntry[]>();
  for (const h of list) {
    const arr = byKey.get(h.bannerKey);
    if (arr) arr.push(h);
    else byKey.set(h.bannerKey, [h]);
  }
  const groups: BannerGroup[] = [];
  const seen = new Set<string>();
  for (const o of order) {
    const entries = byKey.get(o.key);
    if (entries && entries.length) {
      groups.push({ key: o.key, label: o.label, entries, total: entries.length });
      seen.add(o.key);
    }
  }
  for (const [key, entries] of byKey) {
    if (!seen.has(key)) groups.push({ key, label: {}, entries, total: entries.length });
  }
  return groups;
}

// capGroups spends a per-column visible budget across groups in order: render each
// group fully until the budget runs out, truncating the group it runs out in. `total`
// is preserved so the sub-header still shows the real group size. This caps WITHIN a
// dominant group (e.g. 角色) too, so the lazy window isn't defeated by one big group.
export function capGroups(groups: BannerGroup[], budget: number): BannerGroup[] {
  const out: BannerGroup[] = [];
  let left = budget;
  for (const g of groups) {
    if (left <= 0) break;
    const entries = g.entries.slice(0, left);
    left -= entries.length;
    out.push({ key: g.key, label: g.label, entries, total: g.total });
  }
  return out;
}
