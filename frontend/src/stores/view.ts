import { defineStore } from 'pinia';

type ContentView = 'detail' | 'grid';
type ViewMode = ContentView | 'settings';

// 'detail' is the home view the app boots into; toggling settings off returns
// here rather than to whatever view settings was opened from.
const HOME: ContentView = 'detail';

export const useViewStore = defineStore('view', {
  state: () => ({
    sidebarCollapsed: false,
    viewMode: HOME as ViewMode,
    homeTab: 'overview' as 'overview' | 'gacha',
  }),
  getters: {
    // Settings is a peer view, mutually exclusive with grid/detail (it no
    // longer floats over everything). Exposed as a boolean for components that
    // think in open/closed terms.
    settingsOpen: (s): boolean => s.viewMode === 'settings',
  },
  actions: {
    toggleSidebar() { this.sidebarCollapsed = !this.sidebarCollapsed; },
    setView(v: ContentView) { this.viewMode = v; },
    openSettings() { this.viewMode = 'settings'; },
    closeSettings() { if (this.viewMode === 'settings') this.viewMode = HOME; },
    toggleSettings() { this.viewMode = this.viewMode === 'settings' ? HOME : 'settings'; },
    setHomeTab(t: 'overview' | 'gacha') { this.homeTab = t; },
  },
});
