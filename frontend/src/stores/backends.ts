import { defineStore } from 'pinia';
import { ListBackends } from '../../wailsjs/go/app/App';

export type BackendStatus = {
  backend_id: string;
  display_name: Record<string, string>;
  status: 'ok' | 'path_unset' | 'launcher_missing' | 'empty' | 'error';
  detail?: string;
};

export const useBackendsStore = defineStore('backends', {
  state: () => ({
    backends: [] as BackendStatus[],
  }),
  actions: {
    async load() {
      // Go's BackendStatus.Status is `string` — the cast narrows to the
      // literal union since the App layer only emits these five values.
      this.backends = (await ListBackends()) as BackendStatus[];
    },
  },
});
