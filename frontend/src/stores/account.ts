import { defineStore } from 'pinia';
import { ListGameAccounts, SetAccountLabel } from '../../wailsjs/go/app/App';

export interface Account { id: string; uid: string; label: string; email: string; username: string; active: boolean; }
interface AcctState { accounts: Account[]; selectedId: string; loaded: boolean; loading: boolean; }

function blank(): AcctState { return { accounts: [], selectedId: '', loaded: false, loading: false }; }

// accountPrimary is the display identity: custom label, else email, else KRSDK name.
export function accountPrimary(a: Account): string { return a.label || a.email || a.username; }

export const useAccountStore = defineStore('account', {
  state: () => ({ byGid: {} as Record<string, AcctState> }),
  getters: {
    accountsFor: (s) => (gid: string): Account[] => s.byGid[gid]?.accounts ?? [],
    supportedFor: (s) => (gid: string): boolean => (s.byGid[gid]?.accounts.length ?? 0) > 0,
    selectedFor: (s) => (gid: string): Account | undefined => {
      const st = s.byGid[gid];
      return st ? st.accounts.find((a) => a.id === st.selectedId) : undefined;
    },
    activeFor: (s) => (gid: string): Account | undefined => s.byGid[gid]?.accounts.find((a) => a.active),
  },
  actions: {
    async load(gid: string) {
      if (!gid) return;
      this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: true };
      try {
        const accounts = (await ListGameAccounts(gid)) as unknown as Account[];
        const prev = this.byGid[gid]?.selectedId ?? '';
        const active = accounts.find((a) => a.active);
        // default / self-heal: keep an explicit selection if still present, else
        // fall back to the written-active account.
        const selectedId = accounts.some((a) => a.id === prev) ? prev : (active?.id ?? '');
        this.byGid[gid] = { accounts, selectedId, loaded: true, loading: false };
      } catch {
        this.byGid[gid] = { accounts: [], selectedId: '', loaded: true, loading: false };
      }
    },
    // select sets the UI intent only — NO disk write (that happens on Launch).
    select(gid: string, id: string) {
      const st = this.byGid[gid];
      if (st) st.selectedId = id;
    },
    async setLabel(gid: string, id: string, label: string) {
      try { await SetAccountLabel(gid, id, label); } catch { /* local, best-effort */ }
      await this.load(gid);
    },
    reset() { this.byGid = {}; },
  },
});
