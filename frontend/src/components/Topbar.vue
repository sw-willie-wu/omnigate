<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { i18n, setLang } from '../i18n';
import { SetLanguage } from '../../wailsjs/go/app/App';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';
import { useResumePrompt } from '../composables/useResumePrompt';
import { refreshAll } from '../composables/useRefreshAll';
import SettingsPanel from './SettingsPanel.vue';

const { t } = useI18n();
const view = useViewStore();
const { pending, pendingCount, resume, dismiss } = useResumePrompt();

const panelOpen = ref(false);
function toggleNotifPanel() { panelOpen.value = !panelOpen.value; }
function closeNotifPanel() { panelOpen.value = false; }

// 2-way toggle: zh-TW ↔ en. zh-CN locale exists but isn't in the toggle.
// Persist the choice so it survives a restart; best-effort (UI already switched).
const cycleLang = () => {
  const next = i18n.global.locale.value === 'zh-TW' ? 'en' : 'zh-TW';
  setLang(next);
  SetLanguage(next).catch((e) => console.warn('persist language failed', e));
};

const onRefresh = async () => {
  try {
    await refreshAll();
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
      <button class="icon-btn notif-btn" :class="{active: pendingCount > 0, open: panelOpen}" @click="toggleNotifPanel" title="Notifications">
        <span class="material-symbols-outlined">{{ pendingCount > 0 ? 'notifications_active' : 'notifications' }}</span>
        <span v-if="pendingCount > 0" class="notif-badge">{{ pendingCount }}</span>
      </button>
      <button class="icon-btn" :class="{active: view.viewMode === 'grid'}" @click="view.setView(view.viewMode === 'grid' ? 'detail' : 'grid')"><span class="material-symbols-outlined">grid_view</span></button>
      <button class="icon-btn" @click="view.openSettings()" title="Settings"><span class="material-symbols-outlined">settings</span></button>
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
    <SettingsPanel />
  </div>
</template>
