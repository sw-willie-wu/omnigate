import { defineStore } from 'pinia';
import { GetGachaSummary, RefreshGacha } from '../../wailsjs/go/app/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

export interface BannerPity { key: string; label: Record<string, string>; current: number; cap: number; nearPity: boolean; }
export interface HeadlineEntry { name: string; itemType: string; bannerKey: string; time: string; count: number; }
export interface GachaSummary {
  supported: boolean; uid: string; totalPulls: number; perBanner: Record<string, number>;
  spendEst: number; currency: string; headlineCnt: number; headlineByType: Record<string, number>;
  avgPity: number; expectedPity: number; luckScore: number; winRate5050: number | null; worstPull: number;
  pity: BannerPity[]; distribution: number[]; recentHeadline: HeadlineEntry[];
}
// One pagination progress tick streamed from the backend during refresh.
export interface GachaProgress { banner: Record<string, string>; page: number; poolIndex: number; poolTotal: number; }
type ErrKind = 'url' | 'other' | null;
interface GachaState { summary: GachaSummary | null; loading: boolean; errKind: ErrKind; loaded: boolean; progress: GachaProgress | null; }

function blank(): GachaState { return { summary: null, loading: false, errKind: null, loaded: false, progress: null }; }

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
    },
    async load(gid: string) {
      if (!gid) return;
      const cur = this.byGid[gid];
      if (cur && (cur.loaded || cur.loading)) return;
      this.byGid[gid] = { ...blank(), loading: true };
      try {
        const summary = (await GetGachaSummary(gid)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true, progress: null };
      } catch {
        this.byGid[gid] = { summary: null, loading: false, errKind: 'other', loaded: true, progress: null };
      }
    },
    async refresh(gid: string) {
      if (!gid) return;
      this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: true, errKind: null, progress: null };
      try {
        const summary = (await RefreshGacha(gid)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true, progress: null };
      } catch (e) {
        const msg = (e instanceof Error ? e.message : String(e)) || '';
        const kind: ErrKind = msg.includes('url') ? 'url' : 'other';
        this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: false, errKind: kind, loaded: true, progress: null };
      }
    },
    reset() { this.byGid = {}; },
  },
});
