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
            // Prefer the first background with a video; fall back to the first bg's image otherwise.
            const withVideo = bgs.find((b) => b.VideoURL);
            const pick = withVideo ?? bgs[0];
            g.background_url = pick.ImageURL;
            g.background_video = pick.VideoURL;
          }
        } catch (e) {
          console.warn('assets failed', g.id, e);
        }
      }
    },
    async refreshVersionFor(gameID: string) {
      const idx = this.games.findIndex((g) => g.id === gameID);
      if (idx < 0 || !this.games[idx].installed) return;
      try {
        const v = await RefreshVersion(gameID);
        this.games[idx].current_version = v.Current;
        this.games[idx].latest_version = v.Latest;
        this.games[idx].has_predownload = !!v.Predownload;
      } catch (e) {
        console.warn('refreshVersionFor failed', gameID, e);
      }
    },
    async loadAssetsFor(gameID: string) {
      const idx = this.games.findIndex((g) => g.id === gameID);
      if (idx < 0 || !this.games[idx].installed) return;
      try {
        const g = this.games[idx];
        if (!g.icon_url) g.icon_url = await GetIcon(gameID);
        const bgs = await GetBackgrounds(gameID);
        if (bgs.length) {
          const withVideo = bgs.find((b) => b.VideoURL);
          const pick = withVideo ?? bgs[0];
          g.background_url = pick.ImageURL;
          g.background_video = pick.VideoURL;
        }
      } catch (e) {
        console.warn('loadAssetsFor failed', gameID, e);
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
