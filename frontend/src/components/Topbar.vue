<script setup lang="ts">
import { ref, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { i18n, setLang } from '../i18n';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';
import { Refresh } from '../../wailsjs/go/app/App';
import { useResumePrompt } from '../composables/useResumePrompt';

const { t } = useI18n();
const view = useViewStore();
const games = useGamesStore();
const updates = useUpdatesStore();
const { pending, pendingCount, resume, dismiss } = useResumePrompt();

// Bell badge: spinner when any game has an in-flight operation (lower priority than red dot)
const anyInFlight = computed(() => Object.values(updates.byGame).some(s => s.in_flight != null));

const panelOpen = ref(false);
function toggleNotifPanel() { panelOpen.value = !panelOpen.value; }
function closeNotifPanel() { panelOpen.value = false; }

// 2-way toggle: zh-TW ↔ en. zh-CN locale exists but isn't in the toggle.
const cycleLang = () => {
  setLang(i18n.global.locale.value === 'zh-TW' ? 'en' : 'zh-TW');
};

const onRefresh = async () => {
  try {
    await Refresh();
    await games.load();
    await games.refreshVersions();
    await games.loadAssets();
    // Probe for updates so BottomBar [更新 ↓] can appear (spec §1.2.1
    // missing-trigger gap fixed during M3.A Task 18 smoke). Best-effort,
    // parallel; errors swallowed in updates.checkForUpdate.
    await Promise.allSettled(
      games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
    );
  } catch (e) {
    console.error('refresh failed', e);
  }
};
</script>

<template>
  <div class="topbar">
    <div class="toolbar">
      <button class="icon-btn" @click="cycleLang" title="Language"><span class="material-symbols-outlined">translate</span></button>
      <button class="icon-btn" @click="onRefresh" title="Refresh"><span class="material-symbols-outlined">refresh</span></button>
      <button class="icon-btn notif-btn" :class="{active: pendingCount > 0, open: panelOpen, inflight: anyInFlight && pendingCount === 0}" @click="toggleNotifPanel" title="Notifications">
        <span class="material-symbols-outlined">{{ pendingCount > 0 ? 'notifications_active' : 'notifications' }}</span>
        <span v-if="pendingCount > 0" class="notif-badge">{{ pendingCount }}</span>
        <span v-else-if="anyInFlight" class="badge spinner"></span>
      </button>
      <button class="icon-btn" :class="{active: view.viewMode === 'grid'}" @click="view.setView(view.viewMode === 'grid' ? 'detail' : 'grid')"><span class="material-symbols-outlined">grid_view</span></button>
      <button class="icon-btn"><span class="material-symbols-outlined">settings</span></button>
      <button class="icon-btn window-btn" @click="WindowMinimise()" title="Minimize">─</button>
      <button class="icon-btn window-btn close" @click="Quit()" title="Close">×</button>
    </div>
    <Teleport to="body">
      <div v-if="panelOpen" class="notif-panel-backdrop" @click="closeNotifPanel"></div>
      <div v-if="panelOpen" class="notif-panel">
        <div class="notif-panel-header">
          <span>{{ t('notifications.title') }}</span>
          <button class="notif-panel-close" @click="closeNotifPanel" aria-label="Close">×</button>
        </div>
        <div v-if="pending.length === 0" class="notif-empty">{{ t('notifications.empty') }}</div>
        <div v-else class="notif-list">
          <div v-for="item in pending" :key="item.gameID" class="notif-item">
            <div class="notif-msg">{{ item.message }}</div>
            <div class="notif-actions">
              <button class="notif-btn-cancel" @click="dismiss(item.gameID)">{{ t('buttons.cancel') }}</button>
              <button class="notif-btn-ok" @click="resume(item.gameID)">{{ t('buttons.confirm') }}</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
