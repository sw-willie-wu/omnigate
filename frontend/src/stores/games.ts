import { defineStore } from 'pinia';
import { ListGames, RefreshVersion, GetIcon, GetBackgrounds } from '../../wailsjs/go/app/App';

export type GameRow = {
  id: string;
  backend: string;
  display_name: Record<string, string>;
  installed: boolean;
  install_path?: string;
  current_version?: string;
  latest_version?: string;
  has_predownload: boolean;
  icon_url?: string;
  background_url?: string;
  background_video?: string;
};

export const useGamesStore = defineStore('games', {
  state: () => ({
    games: [] as GameRow[],
    selectedID: '' as string,
  }),
  actions: {
    async load() {
      this.games = await ListGames();
      if (!this.selectedID && this.games.length) this.selectedID = this.games[0].id;
    },
    async refreshVersions() {
      for (const g of this.games) {
        if (!g.installed) continue;
        try {
          const v = await RefreshVersion(g.id);
          g.current_version = v.Current;
          g.latest_version = v.Latest;
          g.has_predownload = !!v.Predownload;
        } catch (e) {
          console.warn('version check failed', g.id, e);
        }
      }
    },
    async loadAssets() {
      for (const g of this.games) {
        if (!g.installed) continue;
        try {
          if (!g.icon_url) g.icon_url = await GetIcon(g.id);
          const bgs = await GetBackgrounds(g.id);
          if (bgs.length) {
            g.background_url = bgs[0].ImageURL;
            g.background_video = bgs[0].VideoURL;
          }
        } catch (e) {
          console.warn('assets failed', g.id, e);
        }
      }
    },
    select(id: string) { this.selectedID = id; },
  },
  getters: {
    selected(state): GameRow | undefined {
      return state.games.find((g) => g.id === state.selectedID);
    },
  },
});
