import type { HeadlineEntry, BannerPity } from '../stores/gacha';

// baseKey strips a composite "<base>:<poolId>" key (Endfield PerPool banners) down to its
// base banner key. A bare key (no ':') is returned unchanged → no-op for every other game.
export function baseKey(key: string): string {
  const i = key.indexOf(':');
  return i === -1 ? key : key.slice(0, i);
}

// Banner keys whose high-rarity drops are equipment (weapon-slot), not characters.
// Light cones (HSR) and W-engines (ZZZ) are the weapon equivalents → weapon side;
// WuWa's weapon pools (incl. weapon_exchange 武器新旅換取, collab_weapon 武器聯動) too.
const EQUIP_BANNERS = new Set(['weapon', 'standard_weapon', 'weapon_exchange', 'collab_weapon', 'lightcone', 'wengine']);

export function isEquip(bannerKey: string): boolean {
  return EQUIP_BANNERS.has(baseKey(bannerKey));
}

// The gacha.item_type.* key naming this game's equipment kind, from a banner key
// (lightcone → 光錐, wengine → 音擎, else 武器). Used for the weapon column heading.
export function equipTypeKey(bannerKey: string): 'lightcone' | 'wengine' | 'weapon' {
  const b = baseKey(bannerKey);
  if (b === 'lightcone') return 'lightcone';
  if (b === 'wengine') return 'wengine';
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

// One-shot / finite gacha pools (no ongoing pity): the WuWa beginner banner, the
// 新手自選 selector, the 感恩定向 gift, and the new-account 新旅換取 pools. (Only
// `beginner` exists in non-WuWa games among these.)
const ONE_SHOT_BANNERS = new Set([
  'beginner', 'beginner_choice', 'other', 'char_exchange', 'weapon_exchange',
]);

// isOneShotPool reports whether a banner is a one-shot / finite pool with no ongoing
// pity: the beginner banner, the 新手自選 selector, the 感恩定向 gift, and the new-account
// 新旅換取 pools. Their per-pull pity count has no hard-pity cap to fill a bar against.
export function isOneShotPool(key: string): boolean {
  return ONE_SHOT_BANNERS.has(baseKey(key));
}

// shouldShowPity decides whether to show a banner's pity-progress bar. Show it for any
// banner the account has pulled on, EXCEPT a one-shot/finite pool that has ALREADY
// produced its guaranteed headline 5★ — its pity is spent, so the bar is meaningless.
// A one-shot pool still mid-progress (no headline yet) keeps its bar.
export function shouldShowPity(key: string, pulls: number, hasHeadline: boolean): boolean {
  if (pulls <= 0) return false;
  if (isOneShotPool(key) && hasHeadline) return false;
  return true;
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

export interface PoolSection {
  key: string;
  label: Record<string, string>;
  pity: BannerPity | null;   // leading pity row; null = one-shot or no pity for this pool
  entries: HeadlineEntry[];  // high-star records for this pool (may be empty)
  total: number;             // un-truncated high-star count (sub-header); 0 if none
}

// buildPoolSections merges one column's high-star record groups with its pity rows into
// per-pool sections. Sections follow `order` (the banner/pity order); unmapped banners
// that carry records trail in a catch-all, matching groupByBanner. A section is kept if it
// has a pity row OR at least one (budget-permitting) high-star entry. Pity rows are ALWAYS
// shown and consume NO budget; high-star entries share `budget` across sections in order
// exactly as capGroups does, so the records shown are identical to today's. `pity` must
// already exclude one-shot pools, and its keys are expected to be a subset of `order`
// (the caller passes the per-column subset of sum.pity) — a pity key absent from both
// `order` and the records would not surface a section.
export function buildPoolSections(
  highlights: HeadlineEntry[],
  pity: BannerPity[],
  order: { key: string; label: Record<string, string> }[],
  budget: number,
): PoolSection[] {
  const groups = groupByBanner(highlights, order);
  const groupByKey = new Map(groups.map((g) => [g.key, g]));
  const pityByKey = new Map(pity.map((p) => [p.key, p]));

  // Ordered key list: `order` keys that have a group or a pity row, then any catch-all
  // group keys not in `order` (preserving groupByBanner's trailing order).
  const keys: string[] = [];
  const seen = new Set<string>();
  for (const o of order) {
    if ((groupByKey.has(o.key) || pityByKey.has(o.key)) && !seen.has(o.key)) {
      keys.push(o.key);
      seen.add(o.key);
    }
  }
  for (const g of groups) {
    if (!seen.has(g.key)) { keys.push(g.key); seen.add(g.key); }
  }

  const out: PoolSection[] = [];
  let left = budget;
  for (const k of keys) {
    const g = groupByKey.get(k);
    const p = pityByKey.get(k) ?? null;
    const all = g?.entries ?? [];
    const entries = left > 0 ? all.slice(0, left) : [];
    left -= entries.length;
    if (!p && entries.length === 0) continue; // record-only section trimmed away / empty
    const label = g && Object.keys(g.label).length ? g.label : (p?.label ?? {});
    out.push({ key: k, label, pity: p, entries, total: g?.total ?? 0 });
  }
  return out;
}

export interface BannerPanel {
  base: string;                       // banner base key (e.g. "special", "weapon", "standard")
  label: Record<string, string>;      // base banner title (no 期名)
  topPity: BannerPity | null;         // cross-pool aggregate bar; null = no top bar
  subs: PoolSection[];                // per-期 sections (label = 期名 only), order preserved
}

// splitLabel splits a composeLabel value "<base> - <poolName>" into [base, poolName] per the
// FIRST " - "; a bare label (no " - ") yields [whole, whole] so a non-PerPool panel keeps its
// title and its single sub both show the full banner name.
function splitLabel(label: Record<string, string>): { base: Record<string, string>; pool: Record<string, string> } {
  const base: Record<string, string> = {};
  const pool: Record<string, string> = {};
  for (const [loc, v] of Object.entries(label)) {
    const i = v.indexOf(' - ');
    if (i === -1) {
      base[loc] = v;
      pool[loc] = v;
    } else {
      base[loc] = v.slice(0, i);
      pool[loc] = v.slice(i + 3);
    }
  }
  return { base, pool };
}

// buildBannerPanels groups per-pool sections by their base banner key into one panel each,
// preserving section order. topPityByBase supplies the cross-pool aggregate bar (sourced from
// the RAW sum.pity by the caller); a base absent from the map has no top bar. Each sub's label
// is reduced to the 期名 (the part after " - "); the panel title is the base banner name.
export function buildBannerPanels(
  sections: PoolSection[],
  topPityByBase: Map<string, BannerPity>,
): BannerPanel[] {
  const order: string[] = [];
  const byBase = new Map<string, PoolSection[]>();
  for (const s of sections) {
    const b = baseKey(s.key);
    const arr = byBase.get(b);
    if (arr) arr.push(s);
    else {
      byBase.set(b, [s]);
      order.push(b);
    }
  }
  return order.map((b) => {
    const group = byBase.get(b)!;
    const top = topPityByBase.get(b) ?? null;
    const title = top ? top.label : splitLabel(group[0].label).base;
    const subs = group.map((s) => ({ ...s, label: splitLabel(s.label).pool }));
    return { base: b, label: title, topPity: top, subs };
  });
}

// compactNum keeps values under 10k in full (e.g. "1,200") and abbreviates larger ones as
// K/M (12,800 → "12.8K", 420,640 → "421K", 1,200,000 → "1.2M"): 1 decimal when the
// abbreviated value is under 100, integer at or above. Used for the (potentially large)
// consumed-stones card so it never overflows.
export function compactNum(n: number): string {
  if (n < 10000) return n.toLocaleString();
  const fmt = (v: number) => (v < 100 ? v.toFixed(1) : Math.round(v).toString());
  // 999,500+ would round up to "1000K" → show as M instead.
  return n < 999500 ? fmt(n / 1000) + 'K' : fmt(n / 1000000) + 'M';
}

export interface CardMetrics {
  limCharCnt: number;        // won featured characters (off=false), top-rank
  limWeaponCnt: number;      // won featured weapons (off=false), top-rank
  avgAll: number | null;     // mean pity-count, top-rank, excl ALL one-shot pools
  avgChar: number | null;    // mean acquisition cost per won featured char (incl preceding 歪)
  avgWeapon: number | null;  // mean acquisition cost per won featured weapon (incl preceding 歪)
  hitWins: number;           // top-rank featured (char+weapon), off===false
  hitTotal: number;          // top-rank featured (char+weapon), off + won
  hitRate: number | null;    // hitWins/hitTotal in [0,1], null when hitTotal===0
  distribution: number[];    // 9-bucket pull-count histogram, same population as avgAll
}

// distBucket maps a pull-count to a 9-bucket histogram index (mirrors the backend bucket()):
// 0=1-9, 1=10-19, …, 7=70-79, 8=80+.
function distBucket(count: number): number {
  if (count >= 80) return 8;
  return Math.min(Math.floor(count / 10), 8);
}

// computeCardMetrics derives the eight stat-card numbers from the complete top-rarity
// highlight list. Only rank===top pulls count. A "featured" pull is on a Limited (featured
// or collab) banner that is not a one-shot/finite pool (so WuWa's limited-but-one-shot
// 新旅換取 pools are excluded).
//
// avgAll averages every non-one-shot top-rank pity count (per-5★). The hit rate is over
// featured pulls (off=loss / win). The per-type counts and averages are ACQUISITION-based:
// a featured char won only at the hard pity also cost the lost-50/50 (off) pull(s) before it,
// so each off pull's count rolls into a per-banner `pending` and is added to the next featured
// win's cost; only wins are counted (limCharCnt/limWeaponCnt) and only their acquisition costs
// averaged. Trailing offs (no later win) are discarded. Iterates highlights in reverse
// (they arrive newest-first) so the off→win pairing follows real time.
export function computeCardMetrics(highlights: HeadlineEntry[], top: number): CardMetrics {
  let allSum = 0, allN = 0;
  let charSum = 0, charN = 0, weaponSum = 0, weaponN = 0;
  let hitWins = 0, hitTotal = 0;
  const distribution = new Array(9).fill(0) as number[];
  const pending = new Map<string, number>(); // per-banner off-pull pulls awaiting the next win
  for (let i = highlights.length - 1; i >= 0; i--) { // oldest-first
    const h = highlights[i];
    if (h.rank !== top) continue;
    if (!isOneShotPool(h.bannerKey)) { allSum += h.count; allN++; distribution[distBucket(h.count)]++; }
    if (!(h.limited && !isOneShotPool(h.bannerKey))) continue; // featured only past here
    hitTotal++;
    const roll = (pending.get(h.bannerKey) ?? 0) + h.count;
    if (h.off) {
      pending.set(h.bannerKey, roll); // lost 50/50 → carry these pulls forward
    } else {
      hitWins++;
      pending.set(h.bannerKey, 0);
      if (isEquip(h.bannerKey)) { weaponSum += roll; weaponN++; }
      else { charSum += roll; charN++; }
    }
  }
  const mean = (sum: number, n: number) => (n > 0 ? sum / n : null);
  return {
    limCharCnt: charN, limWeaponCnt: weaponN,
    avgAll: mean(allSum, allN), avgChar: mean(charSum, charN), avgWeapon: mean(weaponSum, weaponN),
    hitWins, hitTotal, hitRate: hitTotal > 0 ? hitWins / hitTotal : null,
    distribution,
  };
}
