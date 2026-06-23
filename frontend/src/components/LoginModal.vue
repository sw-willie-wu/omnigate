<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGachaAccountStore, type GachaAccount } from '../stores/gachaAccount';
import { SetGachaCredential } from '../../wailsjs/go/app/App';

const props = defineProps<{ gameId: string }>();
const emit = defineEmits<{
  (e: 'added', acc: GachaAccount): void;
  (e: 'close'): void;
}>();

const { t } = useI18n();
const gachaAccount = useGachaAccountStore();

const email = ref('');
const password = ref('');
const loading = ref(false);
const error = ref('');

const advanced = ref(false);
const pasteToken = ref('');
const pasteLoading = ref(false);
const pasteError = ref('');

// mapLoginError maps a backend error message to a user-friendly string.
// Uses substring matching — same pattern as classifyErr in gacha.ts.
// Order: specific "bad email/password" / "login failed" checked first;
// "no game role" before the looser "role" fallback; generic otherwise.
function mapLoginError(msg: string): string {
  if (msg.includes('bad email/password') || msg.includes('login failed')) {
    return t('gacha.login.err_bad_creds');
  }
  if (msg.includes('no game role') || msg.includes('role')) {
    return t('gacha.login.err_no_role');
  }
  return `${t('gacha.login.err_generic')} (${msg})`;
}

async function submit() {
  if (!email.value || !password.value || loading.value) return;
  loading.value = true;
  error.value = '';
  try {
    const acc = await gachaAccount.addByLogin(props.gameId, email.value, password.value);
    password.value = ''; // clear before emitting so it's never kept in state
    emit('added', acc);
    emit('close');
  } catch (e: unknown) {
    const msg = (e instanceof Error ? e.message : String(e)) || '';
    error.value = mapLoginError(msg);
  } finally {
    loading.value = false;
  }
}

async function submitPaste() {
  const token = pasteToken.value.trim();
  if (!token || pasteLoading.value) return;
  pasteLoading.value = true;
  pasteError.value = '';
  try {
    await SetGachaCredential(props.gameId, token);
    await gachaAccount.load(props.gameId);
    pasteToken.value = '';
    emit('close');
  } catch (e: unknown) {
    const msg = (e instanceof Error ? e.message : String(e)) || '';
    pasteError.value = `${t('gacha.login.paste_err')} (${msg})`;
  } finally {
    pasteLoading.value = false;
  }
}

function close() {
  password.value = '';  // never leave the password in state
  pasteToken.value = '';
  emit('close');
}

// ESC to close — bind on window while the modal is mounted (same pattern as
// SettingsPanel) because @keydown on the overlay only fires when it has focus.
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') close();
}
onMounted(() => window.addEventListener('keydown', onKeydown));
onUnmounted(() => window.removeEventListener('keydown', onKeydown));
</script>

<template>
  <!-- Teleport to <body> so the overlay escapes any backdrop-filter stacking
       context inside the app-wrap and renders at the top of the z-stack. -->
  <Teleport to="body">
    <div
      class="login-overlay"
      data-test="login-modal"
      @click.self="close"
    >
      <div class="login-panel" role="dialog" aria-modal="true">
        <h2 class="login-title">{{ t('gacha.login.title') }}</h2>

        <div class="login-fields">
          <label class="login-label" for="login-email-input">{{ t('gacha.login.email_label') }}</label>
          <input
            id="login-email-input"
            data-test="login-email"
            type="email"
            v-model="email"
            autocomplete="email"
            :disabled="loading"
            @keydown.enter.prevent="submit"
          />

          <label class="login-label" for="login-password-input">{{ t('gacha.login.password_label') }}</label>
          <input
            id="login-password-input"
            data-test="login-password"
            type="password"
            v-model="password"
            autocomplete="current-password"
            :disabled="loading"
            @keydown.enter.prevent="submit"
          />
        </div>

        <div v-if="error" class="login-error" data-test="login-error">{{ error }}</div>

        <div class="login-actions">
          <button
            class="login-btn login-btn--primary"
            data-test="login-submit"
            :disabled="loading || !email || !password"
            @click="submit"
          >
            {{ loading ? t('gacha.login.signing_in') : t('gacha.login.submit') }}
          </button>
          <button
            class="login-btn"
            data-test="login-cancel"
            :disabled="loading"
            @click="close"
          >
            {{ t('buttons.cancel') }}
          </button>
        </div>

        <!-- Advanced: collapsed disclosure revealing a manual token paste flow. -->
        <details class="login-advanced" :open="advanced" @toggle="advanced = ($event.target as HTMLDetailsElement).open">
          <summary data-test="login-advanced-toggle">{{ t('gacha.login.advanced') }}</summary>
          <div class="login-paste">
            <label class="login-label" for="login-paste-input">{{ t('gacha.login.paste_label') }}</label>
            <textarea
              id="login-paste-input"
              data-test="login-paste-input"
              v-model="pasteToken"
              rows="3"
              :disabled="pasteLoading"
              class="login-paste-input"
            />
            <div v-if="pasteError" class="login-error" data-test="login-paste-error">{{ pasteError }}</div>
            <button
              class="login-btn login-btn--primary"
              data-test="login-paste-submit"
              :disabled="pasteLoading || !pasteToken.trim()"
              @click="submitPaste"
            >
              {{ pasteLoading ? t('gacha.login.paste_submitting') : t('gacha.login.paste_submit') }}
            </button>
          </div>
        </details>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* Full-screen overlay: semi-transparent backdrop dims the app behind the modal. */
.login-overlay {
  position: fixed;
  inset: 0;
  z-index: 2000;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
}

/* Panel chrome — uses var(--glass-modal) to match the notif-panel and
   game-config-popover frosted style (highest glass elevation in the app). */
.login-panel {
  background: var(--glass-modal);
  backdrop-filter: blur(var(--glass-modal-blur));
  -webkit-backdrop-filter: blur(var(--glass-modal-blur));
  border: 1px solid var(--border-strong);
  border-radius: 14px;
  padding: 28px 32px;
  min-width: 360px;
  max-width: 440px;
  width: 100%;
  box-shadow: 0 24px 56px -14px rgba(0, 0, 0, 0.85);
}

.login-title {
  margin: 0 0 20px;
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.75);
}

.login-fields {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 16px;
}

.login-label {
  font-size: 11px;
  color: var(--text-2);
  letter-spacing: 0.12em;
  text-transform: uppercase;
  margin-top: 4px;
}

.login-fields input {
  background: rgba(0, 0, 0, 0.35);
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  color: var(--text);
  font-family: inherit;
  font-size: 13px;
  padding: 8px 10px;
  outline: none;
  transition: border-color 0.12s;
}
.login-fields input:focus {
  border-color: rgba(255, 255, 255, 0.5);
}
.login-fields input:disabled {
  opacity: 0.45;
}

.login-error {
  font-size: 12px;
  color: #ff8585;
  margin-bottom: 14px;
  padding: 7px 10px;
  background: rgba(255, 80, 80, 0.10);
  border: 1px solid rgba(255, 80, 80, 0.25);
  border-radius: 7px;
  line-height: 1.4;
}

.login-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.login-btn {
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  color: var(--text);
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.04em;
  padding: 8px 18px;
  transition: background 0.12s;
}
.login-btn:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.14);
}
.login-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.login-btn--primary {
  background: rgba(50, 140, 90, 0.35);
  border-color: rgba(80, 200, 130, 0.4);
  color: #a8f0c8;
}
.login-btn--primary:hover:not(:disabled) {
  background: rgba(50, 140, 90, 0.55);
}

/* Advanced disclosure */
.login-advanced {
  margin-top: 16px;
  border-top: 1px solid var(--line-1);
  padding-top: 12px;
}
.login-advanced summary {
  cursor: pointer;
  font-size: 11px;
  color: var(--text-2);
  letter-spacing: 0.08em;
  user-select: none;
  list-style: none;
  display: flex;
  align-items: center;
  gap: 6px;
}
.login-advanced summary::-webkit-details-marker { display: none; }
.login-advanced summary::before {
  content: '▸';
  font-size: 10px;
  transition: transform 0.15s;
}
.login-advanced[open] summary::before {
  transform: rotate(90deg);
}
.login-advanced summary:hover {
  color: var(--text);
}

.login-paste {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 10px;
}
.login-paste-input {
  background: rgba(0, 0, 0, 0.35);
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  color: var(--text);
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  padding: 7px 9px;
  resize: vertical;
  outline: none;
  transition: border-color 0.12s;
}
.login-paste-input:focus {
  border-color: rgba(255, 255, 255, 0.5);
}
.login-paste-input:disabled {
  opacity: 0.45;
}
</style>
