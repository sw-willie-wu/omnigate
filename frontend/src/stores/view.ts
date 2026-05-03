import { defineStore } from 'pinia';

export const useViewStore = defineStore('view', {
  state: () => ({
    sidebarCollapsed: false,
    viewMode: 'detail' as 'detail' | 'grid',
  }),
  actions: {
    toggleSidebar() { this.sidebarCollapsed = !this.sidebarCollapsed; },
    setView(v: 'detail' | 'grid') { this.viewMode = v; },
  },
});
