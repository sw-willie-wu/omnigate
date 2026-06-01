<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';
import { GetSettings, UpdateSettings, BrowseForDirectory } from '../../wailsjs/go/app/App';
import { refreshAll } from '../composables/useRefreshAll';

const { t } = useI18n();
const view = useViewStore();
const updates = useUpdatesStore();

const draft = ref<any>(null);
const saveError = ref('');
const saving = ref(false);

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
      // Window-level ESC: a panel-local @keydown only fires when the panel has
      // focus, which it doesn't on open — so bind on window while open.
      window.addEventListener('keydown', onKeydown);
      draft.value = JSON.parse(JSON.stringify(await GetSettings()));
    } else {
      window.removeEventListener('keydown', onKeydown);
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

async function onSave() {
  if (saveDisabled.value) return;
  saving.value = true;
  saveError.value = '';
  try {
    await UpdateSettings(draft.value);
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
  <Teleport to="body">
    <Transition name="settings-fade">
      <div v-if="view.settingsOpen && draft" class="settings-overlay" @click.self="onCancel">
        <div class="settings-card">
      <div class="settings-header">
        <span>{{ t('settings.title') }}</span>
        <button class="settings-close" @click="onCancel" aria-label="Close">×</button>
      </div>

      <div class="settings-body">
        <!-- HoYoverse -->
        <div class="settings-section">
          <div class="settings-section-title">{{ t('settings.backend.hoyoverse') }}</div>
          <label class="settings-label">{{ t('settings.path_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Hoyoverse.Path" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Hoyoverse.Path = p), draft.Backends.Hoyoverse.Path)">{{ t('settings.browse') }}</button>
          </div>
          <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Hoyoverse.TempDir" :placeholder="t('settings.tempdir_hint')" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Hoyoverse.TempDir = p), draft.Backends.Hoyoverse.TempDir)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="draft.Backends.Hoyoverse.TempDir = ''">{{ t('settings.clear') }}</button>
          </div>
        </div>

        <!-- Kuro -->
        <div class="settings-section">
          <div class="settings-section-title">{{ t('settings.backend.kurogames') }}</div>
          <label class="settings-label">{{ t('settings.path_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Kurogames.Path" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Kurogames.Path = p), draft.Backends.Kurogames.Path)">{{ t('settings.browse') }}</button>
          </div>
          <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Kurogames.TempDir" :placeholder="t('settings.tempdir_hint')" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Kurogames.TempDir = p), draft.Backends.Kurogames.TempDir)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="draft.Backends.Kurogames.TempDir = ''">{{ t('settings.clear') }}</button>
          </div>
        </div>

        <!-- Hypergryph -->
        <div class="settings-section">
          <div class="settings-section-title">{{ t('settings.backend.hypergryph') }}</div>
          <label class="settings-label">{{ t('settings.path_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Hypergryph.Path" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Hypergryph.Path = p), draft.Backends.Hypergryph.Path)">{{ t('settings.browse') }}</button>
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
      </div>
    </Transition>
  </Teleport>
</template>
