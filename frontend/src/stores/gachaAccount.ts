import { defineStore } from 'pinia';
import {
  ListGachaAccounts,
  AddGachaAccountByLogin,
  SelectGachaAccount,
  SetGachaAccountLabel,
  DeleteGachaAccount,
  SetGachaCredential,
} from '../../wailsjs/go/app/App';

export interface GachaAccount {
  id: string;
  uid: string;
  label: string;
  email: string;
  customLabel: string;
  active: boolean;
}

interface GachaAcctState {
  accounts: GachaAccount[];
  selectedId: string;
  loaded: boolean;
  loading: boolean;
}

function blank(): GachaAcctState {
  return { accounts: [], selectedId: '', loaded: false, loading: false };
}

// gachaAccountPrimary is the display identity: custom label > label > email > uid.
export function gachaAccountPrimary(a: GachaAccount): string {
  return a.customLabel || a.label || a.email || a.uid;
}

export const useGachaAccountStore = defineStore('gachaAccount', {
  state: () => ({ byGid: {} as Record<string, GachaAcctState> }),
  getters: {
    accountsFor: (s) => (gid: string): GachaAccount[] => s.byGid[gid]?.accounts ?? [],
    selectedFor: (s) => (gid: string): GachaAccount | undefined => {
      const st = s.byGid[gid];
      return st ? st.accounts.find((a) => a.id === st.selectedId) : undefined;
    },
    activeFor: (s) => (gid: string): GachaAccount | undefined =>
      s.byGid[gid]?.accounts.find((a) => a.active),
  },
  actions: {
    async load(gid: string) {
      if (!gid) return;
      this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: true };
      try {
        const accounts = (await ListGachaAccounts(gid)) as unknown as GachaAccount[];
        const prev = this.byGid[gid]?.selectedId ?? '';
        const active = accounts.find((a) => a.active);
        // Keep an explicit selection if still present, else fall back to the active account.
        const selectedId = accounts.some((a) => a.id === prev) ? prev : (active?.id ?? '');
        this.byGid[gid] = { accounts, selectedId, loaded: true, loading: false };
      } catch {
        this.byGid[gid] = { accounts: [], selectedId: '', loaded: true, loading: false };
      }
    },
    async select(gid: string, id: string) {
      const st = this.byGid[gid];
      if (st) st.selectedId = id;
      try { await SelectGachaAccount(gid, id); } catch { /* best-effort */ }
    },
    async addByLogin(gid: string, email: string, password: string): Promise<GachaAccount> {
      const acc = (await AddGachaAccountByLogin(gid, email, password)) as unknown as GachaAccount;
      await this.load(gid);
      if (this.byGid[gid]) this.byGid[gid].selectedId = acc.id;
      return acc;
    },
    async addByPaste(gid: string, token: string): Promise<GachaAccount> {
      const acc = (await SetGachaCredential(gid, token)) as unknown as GachaAccount;
      await this.load(gid);
      if (this.byGid[gid]) this.byGid[gid].selectedId = acc.id;
      return acc;
    },
    async setLabel(gid: string, id: string, label: string) {
      try { await SetGachaAccountLabel(gid, id, label); } catch { /* best-effort */ }
      await this.load(gid);
    },
    async remove(gid: string, id: string) {
      try { await DeleteGachaAccount(gid, id); } catch { /* best-effort */ }
      await this.load(gid);
    },
    reset() { this.byGid = {}; },
  },
});
