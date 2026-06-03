import { defineStore } from 'pinia';
import { GetNews } from '../../wailsjs/go/app/App';

export type NewsCategory = 'announce' | 'activity' | 'info';

export interface NewsItem {
  title: string;
  category: NewsCategory;
  date: string;
  url: string;
  thumbnail?: string;
}

interface NewsState {
  items: NewsItem[];
  loading: boolean;
  error: boolean;
  loaded: boolean;
}

function blank(): NewsState {
  return { items: [], loading: false, error: false, loaded: false };
}

export const useNewsStore = defineStore('news', {
  state: () => ({
    byGid: {} as Record<string, NewsState>,
  }),
  getters: {
    stateFor: (state) => (gid: string): NewsState => state.byGid[gid] ?? blank(),
    itemsFor: (state) => (gid: string): NewsItem[] => state.byGid[gid]?.items ?? [],
  },
  actions: {
    async load(gid: string) {
      if (!gid) return;
      const cur = this.byGid[gid];
      if (cur && (cur.loaded || cur.loading)) return; // lazy: skip if loaded/in-flight
      this.byGid[gid] = { items: [], loading: true, error: false, loaded: false };
      try {
        const items = (await GetNews(gid)) as unknown as NewsItem[];
        this.byGid[gid] = { items: items ?? [], loading: false, error: false, loaded: true };
      } catch {
        this.byGid[gid] = { items: [], loading: false, error: true, loaded: true };
      }
    },
    reset() {
      this.byGid = {};
    },
  },
});
