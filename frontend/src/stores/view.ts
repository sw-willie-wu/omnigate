import { defineStore } from 'pinia';

type ContentView = 'detail' | 'grid';
type ViewMode = ContentView | 'settings';

export const useViewStore = defineStore('view', {
  state: () => ({
    sidebarCollapsed: false,
    viewMode: 'detail' as ViewMode,
    // The content view to return to when settings is toggled off.
    prevView: 'detail' as ContentView,
  }),
  getters: {
    // Settings is a peer view, mutually exclusive with grid/detail (it no
    // longer floats over everything). Exposed as a boolean for components that
    // think in open/closed terms.
    settingsOpen: (s): boolean => s.viewMode === 'settings',
  },
  actions: {
    toggleSidebar() { this.sidebarCollapsed = !this.sidebarCollapsed; },
    setView(v: ContentView) { this.viewMode = v; this.prevView = v; },
    openSettings() {
      if (this.viewMode !== 'settings') this.prevView = this.viewMode;
      this.viewMode = 'settings';
    },
    closeSettings() {
      if (this.viewMode === 'settings') this.viewMode = this.prevView;
    },
    toggleSettings() {
      if (this.viewMode === 'settings') this.closeSettings();
      else this.openSettings();
    },
  },
});
