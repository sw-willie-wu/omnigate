import { defineStore } from 'pinia';

export const useViewStore = defineStore('view', {
  state: () => ({
    sidebarCollapsed: false,
    viewMode: 'detail' as 'detail' | 'grid',
    settingsOpen: false,
  }),
  actions: {
    toggleSidebar() { this.sidebarCollapsed = !this.sidebarCollapsed; },
    setView(v: 'detail' | 'grid') { this.viewMode = v; },
    openSettings() { this.settingsOpen = true; },
    closeSettings() { this.settingsOpen = false; },
  },
});
