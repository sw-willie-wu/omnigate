<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';
import { GetSettings, UpdateSettings, BrowseForDirectory } from '../../wailsjs/go/app/App';
import { refreshAll } from '../composables/useRefreshAll';
import { i18n, setLang } from '../i18n';

const { t } = useI18n();
const view = useViewStore();
const updates = useUpdatesStore();

const draft = ref<any>(null);
const saveError = ref('');
const saving = ref(false);

let openLang: 'zh-TW' | 'zh-CN' | 'en' = 'zh-TW';
let savedThisSession = false;

const anyInFlight = computed(() =>
  Object.values(updates.byGame).some((s: any) => s?.in_flight != null),
);
const saveDisabled = computed(() => saving.value || anyInFlight.value || !draft.value);

// Load a fresh draft each time the drawer opens; discard on close.
watch(
  () => view.settingsOpen,
  async (open) => {
    if (open) {
      saveError.value = '';
      savedThisSession = false;
      openLang = i18n.global.locale.value as typeof openLang;
      // Window-level ESC: a panel-local @keydown only fires when the panel has
      // focus, which it doesn't on open — so bind on window while open.
      window.addEventListener('keydown', onKeydown);
      draft.value = JSON.parse(JSON.stringify(await GetSettings()));
    } else {
      window.removeEventListener('keydown', onKeydown);
      if (!savedThisSession && i18n.global.locale.value !== openLang) {
        setLang(openLang);
      }
      draft.value = null;
    }
  },
  { immediate: true },
);

onUnmounted(() => window.removeEventListener('keydown', onKeydown));

async function browse(setter: (p: string) => void, current: string) {
  try {
    const p = await BrowseForDirectory(current || '');
    if (p) setter(p);
  } catch (e) {
    console.error('BrowseForDirectory failed', e);
  }
}

function onLangChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value as 'zh-TW' | 'zh-CN' | 'en';
  draft.value.App.Language = v;
  setLang(v);
}

async function onSave() {
  if (saveDisabled.value) return;
  saving.value = true;
  saveError.value = '';
  try {
    await UpdateSettings(draft.value);
    savedThisSession = true;
    await refreshAll();
    view.closeSettings();
  } catch (e: any) {
    saveError.value = t('settings.save_error', { detail: e?.message ?? String(e) });
  } finally {
    saving.value = false;
  }
}

function onCancel() {
  view.closeSettings();
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') onCancel();
}
</script>

<template>
  <div v-if="view.settingsOpen && draft" class="settings-view">
      <div class="settings-body">
        <div class="settings-group">
          <div class="grid-section-label"><span>{{ t('settings.general') }}</span></div>

          <label class="settings-label">{{ t('settings.language_label') }}</label>
          <div class="settings-row">
            <select data-test="settings-language" :value="draft.App.Language" @change="onLangChange">
              <option value="zh-TW">繁體中文</option>
              <option value="zh-CN">简体中文</option>
              <option value="en">English</option>
            </select>
          </div>

          <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
          <div class="settings-row">
            <input type="text" data-test="settings-tempdir" v-model="draft.App.TempDir" :placeholder="t('settings.tempdir_hint')" />
            <button class="settings-browse" @click="browse((p) => (draft.App.TempDir = p), draft.App.TempDir)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="draft.App.TempDir = ''">{{ t('settings.clear') }}</button>
          </div>
        </div>

        <div v-if="saveError" class="settings-error">{{ saveError }}</div>
      </div>

      <div class="settings-footer">
        <button class="settings-btn-cancel" data-test="settings-cancel" @click="onCancel">{{ t('settings.cancel') }}</button>
        <button
          class="settings-btn-save"
          data-test="settings-save"
          :disabled="saveDisabled"
          :title="anyInFlight ? t('settings.save_disabled_inflight') : undefined"
          @click="onSave"
        >{{ t('settings.save') }}</button>
      </div>
  </div>
</template>
