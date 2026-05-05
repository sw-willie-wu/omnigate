import { defineStore } from 'pinia';
import { useGamesStore } from './games';
import {
  StartUpdate,
  StartPredownload,
  CancelInFlight,
  ApplyPredownload,
  RemovePredownload,
  DismissError,
  ResumeInterrupted,
  UpdateStatusAll,
} from '../../wailsjs/go/app/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

export type Phase = 'download' | 'apply';
export type PlanKind = 'update' | 'predownload';

export type UpdateError = {
  code: string;
  params?: Record<string, string | number>;
  retryable: boolean;
};

export type UpdatePlan = {
  kind: PlanKind;
  manifest_etag: string;
  version: string;
  total_bytes: number;
};

export type InFlightSnapshot = {
  kind: PlanKind;
  phase: Phase;
  current: number;
  total: number;
  version: string;
  started_at: string;
};

export type GameUpdateSnapshot = {
  available_update?: UpdatePlan | null;
  available_predl?: UpdatePlan | null;
  in_flight?: InFlightSnapshot | null;
  last_error?: UpdateError | null;
  predl_ready?: UpdatePlan | null;
};

// justCompletedUpdate (spec §3.8): true iff transitioning from in-flight
// PlanUpdate apply → idle with no error. Used to trigger asset refresh
// in useGamesStore.
function justCompletedUpdate(prev: GameUpdateSnapshot | undefined, snap: GameUpdateSnapshot): boolean {
  if (!prev) return false;
  if (!prev.in_flight) return false;
  if (prev.in_flight.kind !== 'update') return false;
  if (prev.in_flight.phase !== 'apply') return false;
  if (snap.in_flight) return false;
  if (snap.last_error) return false;
  return true;
}

export const useUpdatesStore = defineStore('updates', {
  state: () => ({
    byGame: {} as Record<string, GameUpdateSnapshot>,
    pendingFrame: null as number | null,
    pendingPatches: {} as Record<string, GameUpdateSnapshot>,
  }),

  actions: {
    async loadAll() {
      // Spec §3.3: called exactly ONCE on store mount; subsequent updates
      // arrive via push events.
      this.byGame = (await UpdateStatusAll()) as Record<string, GameUpdateSnapshot>;
    },

    bind() {
      // Single listener (spec §3.8): updates.ts owns the event subscription.
      // useGamesStore must NOT also subscribe.
      EventsOn('update:changed', (gameID: string, snap: GameUpdateSnapshot) => {
        this.pendingPatches[gameID] = snap;
        if (this.pendingFrame == null) {
          this.pendingFrame = requestAnimationFrame(() => {
            const games = useGamesStore();
            for (const [id, s] of Object.entries(this.pendingPatches)) {
              const prev = this.byGame[id];
              this.byGame[id] = s;
              if (justCompletedUpdate(prev, s)) {
                games.refreshVersionFor(id);
                games.loadAssetsFor(id);
              }
            }
            this.pendingPatches = {};
            this.pendingFrame = null;
          });
        }
      });
    },

    // RPC wrappers — frontend components call these
    async startUpdate(gameID: string): Promise<void> { await StartUpdate(gameID); },
    async startPredownload(gameID: string): Promise<void> { await StartPredownload(gameID); },
    async cancelInFlight(gameID: string): Promise<void> { await CancelInFlight(gameID); },
    async applyPredownload(gameID: string): Promise<void> { await ApplyPredownload(gameID); },
    async removePredownload(gameID: string): Promise<void> { await RemovePredownload(gameID); },
    async dismissError(gameID: string): Promise<void> { await DismissError(gameID); },
    async resumeInterrupted(gameID: string): Promise<void> { await ResumeInterrupted(gameID); },
  },

  getters: {
    forGame: (state) => (gameID: string): GameUpdateSnapshot | null => {
      return state.byGame[gameID] ?? null;
    },
  },
});
