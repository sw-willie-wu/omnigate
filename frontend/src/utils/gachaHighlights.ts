import type { HeadlineEntry } from '../stores/gacha';

// Banner keys whose high-rarity drops are equipment (weapon-slot), not characters.
// Light cones (HSR) and W-engines (ZZZ) are the weapon equivalents → weapon side.
const EQUIP_BANNERS = new Set(['weapon', 'standard_weapon', 'lightcone', 'wengine']);

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
