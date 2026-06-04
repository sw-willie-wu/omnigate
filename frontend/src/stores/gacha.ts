import { defineStore } from 'pinia';
import { GetGachaSummary, RefreshGacha } from '../../wailsjs/go/app/App';

export interface BannerPity { key: string; label: Record<string, string>; current: number; cap: number; nearPity: boolean; }
export interface HeadlineEntry { name: string; itemType: string; bannerKey: string; time: string; count: number; }
export interface GachaSummary {
  supported: boolean; uid: string; totalPulls: number; perBanner: Record<string, number>;
  spendEst: number; currency: string; headlineCnt: number; headlineByType: Record<string, number>;
  avgPity: number; expectedPity: number; luckScore: number; winRate5050: number | null; worstPull: number;
  pity: BannerPity[]; distribution: number[]; recentHeadline: HeadlineEntry[];
}
type ErrKind = 'url' | 'other' | null;
interface GachaState { summary: GachaSummary | null; loading: boolean; errKind: ErrKind; loaded: boolean; }

function blank(): GachaState { return { summary: null, loading: false, errKind: null, loaded: false }; }

export const useGachaStore = defineStore('gacha', {
  state: () => ({ byGid: {} as Record<string, GachaState> }),
  getters: {
    stateFor: (s) => (gid: string): GachaState => s.byGid[gid] ?? blank(),
  },
  actions: {
    async load(gid: string) {
      if (!gid) return;
      const cur = this.byGid[gid];
      if (cur && (cur.loaded || cur.loading)) return;
      this.byGid[gid] = { ...blank(), loading: true };
      try {
        const summary = (await GetGachaSummary(gid)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true };
      } catch {
        this.byGid[gid] = { summary: null, loading: false, errKind: 'other', loaded: true };
      }
    },
    async refresh(gid: string) {
      if (!gid) return;
      this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: true, errKind: null };
      try {
        const summary = (await RefreshGacha(gid)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true };
      } catch (e) {
        const msg = (e instanceof Error ? e.message : String(e)) || '';
        const kind: ErrKind = msg.includes('url') ? 'url' : 'other';
        this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: false, errKind: kind, loaded: true };
      }
    },
    reset() { this.byGid = {}; },
  },
});
