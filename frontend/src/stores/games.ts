import { defineStore } from 'pinia';
import { ListGames, RefreshVersion, GetIcon, GetBackgrounds, GetCustomBackground, SetGameOverride, ClearGameOverride, RefreshGame, Launch, GameAccountKind } from '../../wailsjs/go/app/App';

export type GameRow = {
  id: string;
  backend: string;
  display_name: Record<string, string>;
  installed: boolean;
  install_path?: string;
  current_version?: string;
  latest_version?: string;
  // Backend-computed (RefreshVersion → versionNewer): the ONLY source of the
  // "update available" state — never compare version strings in the frontend.
  update_available?: boolean;
  has_predownload: boolean;
  icon_url?: string;
  backgrounds?: { image: string; video: string }[];
  bgIndex?: number;
  resolved_path?: string;
  path_source?: string;
  override_path?: string;
  last_played?: string;
};

export const useGamesStore = defineStore('games', {
  state: () => ({
    games: [] as GameRow[],
    selectedID: '' as string,
    _customBg: {} as Record<string, string>,
    // _accountKind[gid]: undefined = not yet fetched; value = cached RPC result.
    _accountKind: {} as Record<string, 'switcher' | 'credential' | 'none'>,
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
          g.update_available = !!v.UpdateAvailable;
          g.has_predownload = !!v.Predownload;
        } catch (e) {
          console.warn('version check failed', g.id, e);
        }
      }
    },
    _randomIndex(len: number): number {
      return len > 0 ? Math.floor(Math.random() * len) : 0;
    },
    // _customBg[id]: undefined = not fetched; '' = fetched, none (or a transient
    // GetCustomBackground error — treated as "no custom" until invalidateCustomBg,
    // so we fail closed to the official backgrounds rather than refetch-storm);
    // url = custom background in effect.
    async _applyCustomBg(g: GameRow): Promise<boolean> {
      if (this._customBg[g.id] === undefined) {
        try { this._customBg[g.id] = await GetCustomBackground(g.id); }
        catch { this._customBg[g.id] = ''; }
      }
      const url = this._customBg[g.id];
      if (url) { g.backgrounds = [{ image: url, video: '' }]; return true; }
      return false;
    },
    invalidateCustomBg() { this._customBg = {}; },
    // Load icon + backgrounds[] + reseed bgIndex for one row. loadAssets and
    // loadAssetsFor both delegate here so the sequence stays in lockstep.
    async _loadAssetsForRow(g: GameRow) {
      if (!g.icon_url) g.icon_url = await GetIcon(g.id);
      if (!(await this._applyCustomBg(g))) {
        const bgs = await GetBackgrounds(g.id);
        g.backgrounds = bgs.map((b) => ({ image: b.ImageURL, video: b.VideoURL }));
      }
      g.bgIndex = this._randomIndex(g.backgrounds?.length ?? 0);
    },
    async loadAssets() {
      for (const g of this.games) {
        if (!g.installed) continue;
        try {
          await this._loadAssetsForRow(g);
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
        this.games[idx].update_available = !!v.UpdateAvailable;
        this.games[idx].has_predownload = !!v.Predownload;
      } catch (e) {
        console.warn('refreshVersionFor failed', gameID, e);
      }
    },
    async loadAssetsFor(gameID: string) {
      const idx = this.games.findIndex((g) => g.id === gameID);
      if (idx < 0 || !this.games[idx].installed) return;
      try {
        await this._loadAssetsForRow(this.games[idx]);
      } catch (e) {
        console.warn('loadAssetsFor failed', gameID, e);
      }
    },
    async setOverride(gameID: string, path: string) {
      const row = await SetGameOverride(gameID, path);
      this._replaceRow(row);
    },
    async clearOverride(gameID: string) {
      const row = await ClearGameOverride(gameID);
      this._replaceRow(row);
    },
    async refreshGame(gameID: string) {
      const row = await RefreshGame(gameID);
      this._replaceRow(row);
    },
    _replaceRow(row: GameRow) {
      const idx = this.games.findIndex((g) => g.id === row.id);
      if (idx < 0) return;
      this.games.splice(idx, 1, row);
      if (row.installed) {
        this.refreshVersionFor(row.id);
        this.loadAssetsFor(row.id);
      }
    },
    // Launch a game and optimistically stamp last_played on the live row.
    // Direct field mutation only — NEVER via _replaceRow (that re-fetches and
    // would wipe icon_url/backgrounds/bgIndex). Backend persists the
    // authoritative value to playstate.json; it reaches us on next cold start.
    async launchGame(gameID: string, accountID = '') {
      await Launch(gameID, accountID);
      const row = this.games.find((g) => g.id === gameID);
      if (row) row.last_played = new Date().toISOString();
    },
    select(id: string) { this.selectedID = id; },
    // Fetch GameAccountKind once per gid and cache it. Callers (e.g. AccountChip,
    // GachaPanel) should call ensureAccountKind(gid) on mount, then read the
    // accountKind getter reactively.
    async ensureAccountKind(gid: string) {
      if (!gid || this._accountKind[gid] !== undefined) return;
      try {
        this._accountKind[gid] = (await GameAccountKind(gid)) as 'switcher' | 'credential' | 'none';
      } catch {
        this._accountKind[gid] = 'none';
      }
    },
  },
  getters: {
    selected(state): GameRow | undefined {
      return state.games.find((g) => g.id === state.selectedID);
    },
    // accountKind returns the cached kind for a game; 'none' until ensureAccountKind resolves.
    accountKind: (state) => (gid: string): 'switcher' | 'credential' | 'none' =>
      state._accountKind[gid] ?? 'none',
  },
});
