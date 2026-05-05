import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useUpdatesStore } from '../stores/updates';

// Spec §3.5 row 4 — surface "上次更新中斷" notifications whenever a game's
// LastError is set to interrupted_resume. The Topbar bell-icon dropdown
// reads `pending` to render the list; each item carries the i18n message
// already resolved + handlers to resume / dismiss for that specific game.
export type NotificationItem = {
  gameID: string;
  message: string;
};

export function useResumePrompt() {
  const updates = useUpdatesStore();
  const { t } = useI18n();

  const pending = computed<NotificationItem[]>(() => {
    const out: NotificationItem[] = [];
    for (const [gameID, snap] of Object.entries(updates.byGame)) {
      const err = snap.last_error;
      if (!err || err.code !== 'interrupted_resume') continue;
      const phase = err.params?.phase as string | undefined;
      const wasPredl = err.params?.wasPredl === 'true';
      const key = wasPredl
        ? phase === 'apply'
          ? 'update.errors.interrupted_resume_predl_apply'
          : 'update.errors.interrupted_resume_predl_download'
        : phase === 'apply'
        ? 'update.errors.interrupted_resume_apply'
        : 'update.errors.interrupted_resume_download';
      out.push({ gameID, message: t(key) });
    }
    return out;
  });

  const pendingCount = computed(() => pending.value.length);

  async function resume(gameID: string) {
    await updates.resumeInterrupted(gameID);
  }
  async function dismiss(gameID: string) {
    await updates.dismissError(gameID);
  }

  return { pending, pendingCount, resume, dismiss };
}
