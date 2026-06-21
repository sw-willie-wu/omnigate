import { defineStore } from 'pinia';
import { GetGachaSummary, RefreshGacha } from '../../wailsjs/go/app/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

export interface BannerPity { key: string; label: Record<string, string>; current: number; cap: number; nearPity: boolean; }
export interface HeadlineEntry { name: string; itemType: string; bannerKey: string; time: string; count: number; rank: number; }
export interface GachaSummary {
  supported: boolean; uid: string; activeUnknown: boolean; totalPulls: number; perBanner: Record<string, number>;
  spendEst: number; currency: string; headlineCnt: number; headlineByType: Record<string, number>;
  avgPity: number; expectedPity: number; luckScore: number; winRate5050: number | null; worstPull: number;
  pity: BannerPity[]; distribution: number[]; recentHeadline: HeadlineEntry[]; highlights: HeadlineEntry[];
}
// One pagination progress tick streamed from the backend during refresh.
export interface GachaProgress { banner: Record<string, string>; page: number; poolIndex: number; poolTotal: number; }
type ErrKind = 'url' | 'wrong_account' | 'url_expired' | 'active_unknown' | 'link' | 'other' | null;
interface GachaState { summary: GachaSummary | null; loading: boolean; errKind: ErrKind; loaded: boolean; progress: GachaProgress | null; }

function blank(): GachaState { return { summary: null, loading: false, errKind: null, loaded: false, progress: null }; }

// classifyErr maps a backend error's message (Wails serializes Go errors as the
// .Error() string) to an ErrKind. Order matters: credential MUST come first so
// "gacha credential expired" → 'link' (not 'url_expired'); the specific tokens
// come before the loose 'url' so the legacy "url unavailable" stays 'url' while
// "convene url expired" becomes 'url_expired'.
export function classifyErr(msg: string): ErrKind {
  if (msg.includes('credential')) return 'link';
  if (msg.includes('different account')) return 'wrong_account';
  if (msg.includes('active account unknown')) return 'active_unknown';
  if (msg.includes('expired')) return 'url_expired';
  if (msg.includes('url')) return 'url';
  return 'other';
}

let bound = false;

export const useGachaStore = defineStore('gacha', {
  state: () => ({ byGid: {} as Record<string, GachaState> }),
  getters: {
    stateFor: (s) => (gid: string): GachaState => s.byGid[gid] ?? blank(),
  },
  actions: {
    // bind subscribes once to backend refresh-progress events (idempotent).
    bind() {
      if (bound) return;
      bound = true;
      EventsOn('gacha:progress', (gid: string, p: GachaProgress) => {
        const s = this.byGid[gid];
        if (s) s.progress = p;
      });
      EventsOn('gacha:linked', (gid: string) => {
        this.refresh(gid);
      });
    },
    async load(gid: string, accountID = '') {
      if (!gid) return;
      const cur = this.byGid[gid];
      if (cur && (cur.loaded || cur.loading)) return;
      this.byGid[gid] = { ...blank(), loading: true };
      try {
        const summary = (await GetGachaSummary(gid, accountID)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true, progress: null };
      } catch (e) {
        const msg = (e instanceof Error ? e.message : String(e)) || '';
        this.byGid[gid] = { ...blank(), loading: false, errKind: classifyErr(msg), loaded: true };
      }
    },
    async refresh(gid: string, accountID = '') {
      if (!gid) return;
      this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: true, errKind: null, progress: null };
      try {
        const summary = (await RefreshGacha(gid, accountID)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true, progress: null };
      } catch (e) {
        const msg = (e instanceof Error ? e.message : String(e)) || '';
        const kind: ErrKind = classifyErr(msg);
        this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: false, errKind: kind, loaded: true, progress: null };
      }
    },
    // reload forces a fresh GetGachaSummary read, bypassing the `loaded` guard
    // (used after an account selection so the board re-resolves the chosen uid).
    async reload(gid: string, accountID = '') {
      if (!gid) return;
      this.byGid[gid] = { ...blank(), loading: true };
      try {
        const summary = (await GetGachaSummary(gid, accountID)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true, progress: null };
      } catch (e) {
        const msg = (e instanceof Error ? e.message : String(e)) || '';
        this.byGid[gid] = { ...blank(), loading: false, errKind: classifyErr(msg), loaded: true };
      }
    },
    reset() { this.byGid = {}; },
  },
});
