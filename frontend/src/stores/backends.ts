import { defineStore } from 'pinia';
import { ListBackends } from '../../wailsjs/go/app/App';
import { app } from '../../wailsjs/go/models';

export type BackendStatus = app.BackendStatus;

export const useBackendsStore = defineStore('backends', {
  state: () => ({
    backends: [] as BackendStatus[],
  }),
  actions: {
    async load() {
      this.backends = await ListBackends();
    },
  },
});
