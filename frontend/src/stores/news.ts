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

// Cache key includes the UI language: news content is language-specific, so a
// language switch must surface a different cache entry (and refetch) rather
// than reuse the previous-language items.
function cacheKey(gid: string, lang: string): string {
  return `${gid}|${lang}`;
}

export const useNewsStore = defineStore('news', {
  state: () => ({
    byKey: {} as Record<string, NewsState>,
  }),
  getters: {
    stateFor: (state) => (gid: string, lang: string): NewsState => state.byKey[cacheKey(gid, lang)] ?? blank(),
    itemsFor: (state) => (gid: string, lang: string): NewsItem[] => state.byKey[cacheKey(gid, lang)]?.items ?? [],
  },
  actions: {
    async load(gid: string, lang: string) {
      if (!gid) return;
      const k = cacheKey(gid, lang);
      const cur = this.byKey[k];
      if (cur && (cur.loaded || cur.loading)) return; // lazy: skip if loaded/in-flight
      this.byKey[k] = { items: [], loading: true, error: false, loaded: false };
      try {
        const items = (await GetNews(gid, lang)) as unknown as NewsItem[];
        this.byKey[k] = { items: items ?? [], loading: false, error: false, loaded: true };
      } catch {
        this.byKey[k] = { items: [], loading: false, error: true, loaded: true };
      }
    },
    reset() {
      this.byKey = {};
    },
  },
});
