import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';

// Mocks must be declared before the dynamic import of the component so that
// Vitest's hoisting can replace the module. Follow the same pattern as
// AccountChip.spec.ts and GachaBoard.spec.ts.
const addGachaAccountByLogin = vi.fn();
const listGachaAccounts = vi.fn();
const setGachaCredential = vi.fn();
const selectGachaAccount = vi.fn();
const setGachaAccountLabel = vi.fn();
const deleteGachaAccount = vi.fn();

vi.mock('../../../wailsjs/go/app/App', () => ({
  AddGachaAccountByLogin: (...a: unknown[]) => addGachaAccountByLogin(...a),
  ListGachaAccounts: (...a: unknown[]) => listGachaAccounts(...a),
  SetGachaCredential: (...a: unknown[]) => setGachaCredential(...a),
  SelectGachaAccount: (...a: unknown[]) => selectGachaAccount(...a),
  SetGachaAccountLabel: (...a: unknown[]) => setGachaAccountLabel(...a),
  DeleteGachaAccount: (...a: unknown[]) => deleteGachaAccount(...a),
}));

import LoginModal from '../LoginModal.vue';

// Inline i18n for the modal keys (English) so the spec doesn't depend on the
// real locale files being in sync with the component keys.
const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      gacha: {
        login: {
          title: 'Sign in',
          email_label: 'Email',
          password_label: 'Password',
          submit: 'Sign in',
          signing_in: 'Signing in…',
          err_bad_creds: 'Incorrect email or password',
          err_no_role: 'No game role linked',
          err_generic: 'Login failed',
          advanced: 'Advanced: paste token',
          paste_label: 'Paste token here',
          paste_submit: 'Submit',
          paste_submitting: 'Submitting…',
          paste_err: 'Submit failed',
        },
      },
      buttons: { cancel: 'Cancel' },
    },
  },
});

function mountModal() {
  setActivePinia(createPinia());
  return mount(LoginModal, {
    props: { gameId: 'hypergryph/endfield' },
    // stub Teleport so its content renders inline and is queryable
    global: { plugins: [i18n], stubs: { teleport: true } },
  });
}

describe('LoginModal', () => {
  beforeEach(() => {
    addGachaAccountByLogin.mockReset();
    listGachaAccounts.mockReset();
    setGachaCredential.mockReset();
    selectGachaAccount.mockReset();
    setGachaAccountLabel.mockReset();
    deleteGachaAccount.mockReset();
    // load() calls ListGachaAccounts after a successful addByLogin; keep it
    // non-failing so the store's post-login reload doesn't throw.
    listGachaAccounts.mockResolvedValue([]);
  });

  it('typing email + password and clicking submit calls addByLogin exactly once with the right args', async () => {
    const acc = { id: 'ga_1', uid: 'R1', label: '', email: 'e@x', active: true };
    addGachaAccountByLogin.mockResolvedValue(acc);

    const w = mountModal();
    await w.find('[data-test="login-email"]').setValue('e@x');
    await w.find('[data-test="login-password"]').setValue('pw');
    await w.find('[data-test="login-submit"]').trigger('click');
    await flushPromises();

    expect(addGachaAccountByLogin).toHaveBeenCalledTimes(1);
    expect(addGachaAccountByLogin).toHaveBeenCalledWith('hypergryph/endfield', 'e@x', 'pw');
  });

  it('on success emits added and close, and clears the password field', async () => {
    const acc = { id: 'ga_1', uid: 'R1', label: '', email: 'e@x', active: true };
    addGachaAccountByLogin.mockResolvedValue(acc);

    const w = mountModal();
    await w.find('[data-test="login-email"]').setValue('e@x');
    await w.find('[data-test="login-password"]').setValue('pw');
    await w.find('[data-test="login-submit"]').trigger('click');
    await flushPromises();

    expect(w.emitted('added')).toBeTruthy();
    expect(w.emitted('close')).toBeTruthy();
    // Password must be cleared — never kept in state after a successful call
    expect(
      (w.find('[data-test="login-password"]').element as HTMLInputElement).value,
    ).toBe('');
  });

  it('on rejection with a login-failed message shows a friendly error and does NOT emit close', async () => {
    addGachaAccountByLogin.mockRejectedValue(
      new Error('gacha login failed (bad email/password)'),
    );

    const w = mountModal();
    await w.find('[data-test="login-email"]').setValue('e@x');
    await w.find('[data-test="login-password"]').setValue('wrong');
    await w.find('[data-test="login-submit"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-test="login-error"]').exists()).toBe(true);
    expect(w.find('[data-test="login-error"]').text()).toContain('Incorrect email or password');
    // Modal must stay open — no close emitted
    expect(w.emitted('close')).toBeFalsy();
  });

  it('cancel button emits close and clears the password without calling addByLogin', async () => {
    const w = mountModal();
    await w.find('[data-test="login-password"]').setValue('secret');
    await w.find('[data-test="login-cancel"]').trigger('click');

    expect(w.emitted('close')).toBeTruthy();
    expect(
      (w.find('[data-test="login-password"]').element as HTMLInputElement).value,
    ).toBe('');
    expect(addGachaAccountByLogin).not.toHaveBeenCalled();
  });
});
