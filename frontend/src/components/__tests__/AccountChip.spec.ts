import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';

const list = vi.fn();
const setLabel = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGameAccounts: (...a: unknown[]) => list(...a),
  SetAccountLabel: (...a: unknown[]) => setLabel(...a),
}));

import AccountChip from '../AccountChip.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {
  account: {
    switchHint: 'Switch account',
    rename: 'Rename', namePlaceholder: 'Custom name',
    uidPending: 'Available after login', currentlyLoggedIn: 'Logged in',
  } } } });

function mountChip() {
  setActivePinia(createPinia());
  return mount(AccountChip, { props: { gameId: 'kurogames/wutheringwaves' }, global: { plugins: [i18n], stubs: { teleport: true } } });
}

describe('AccountChip', () => {
  beforeEach(() => { list.mockReset(); setLabel.mockReset(); });

  it('renders the selected (default active) account: email primary, uid secondary', async () => {
    list.mockResolvedValue([
      { id: 'A', uid: '700727240', label: '', email: 'a@example.com', username: 'UA', active: true },
      { id: 'B', uid: '', label: '', email: 'b@example.com', username: 'UB', active: false },
    ]);
    const w = mountChip(); await flushPromises();
    expect(w.find('.ident .primary').text()).toBe('a@example.com');
    expect(w.find('.ident .secondary').text()).toBe('700727240');
  });

  it('shows the user label as primary', async () => {
    list.mockResolvedValue([{ id: 'A', uid: '700727240', label: '主帳', email: 'a@example.com', username: 'UA', active: true }]);
    const w = mountChip(); await flushPromises();
    expect(w.find('.ident .primary').text()).toBe('主帳');
  });

  it('shows the login-pending hint when the UID is unknown', async () => {
    list.mockResolvedValue([{ id: 'A', uid: '', label: '', email: 'a@example.com', username: 'UA', active: true }]);
    const w = mountChip(); await flushPromises();
    expect(w.find('.ident .secondary').text()).toContain('Available after login');
  });

  it('hides itself when the backend lacks the capability', async () => {
    list.mockRejectedValue(new Error('account switching not supported for this game'));
    const w = mountChip(); await flushPromises();
    expect(w.find('[data-test="account-chip"]').exists()).toBe(false);
  });

  it('selecting another account updates the chip to that account (no backend switch)', async () => {
    list.mockResolvedValue([
      { id: 'A', uid: '700727240', label: '', email: 'a@example.com', username: 'UA', active: true },
      { id: 'B', uid: '700001181', label: '', email: 'b@example.com', username: 'UB', active: false },
    ]);
    const w = mountChip(); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-opt-B"]').trigger('click');
    await flushPromises();
    expect(w.find('.ident .primary').text()).toBe('b@example.com');
  });

  it('marks the currently-logged-in (active) account in the dropdown', async () => {
    list.mockResolvedValue([
      { id: 'A', uid: '700727240', label: '', email: 'a@example.com', username: 'UA', active: true },
      { id: 'B', uid: '700001181', label: '', email: 'b@example.com', username: 'UB', active: false },
    ]);
    const w = mountChip(); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    expect(w.find('[data-test="account-active-A"]').exists()).toBe(true);
    expect(w.find('[data-test="account-active-B"]').exists()).toBe(false);
  });

  it('renames via the inline input (Enter saves) without switching', async () => {
    list.mockResolvedValue([{ id: 'A', uid: '700727240', label: '', email: 'a@example.com', username: 'UA', active: true }]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip(); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-rename-A"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-A"]');
    await input.setValue('NewName');
    await input.trigger('keydown.enter');
    expect(setLabel).toHaveBeenCalledWith('kurogames/wutheringwaves', 'A', 'NewName');
  });

  it('empty input clears the label', async () => {
    list.mockResolvedValue([{ id: 'A', uid: '700727240', label: '主帳', email: 'a@example.com', username: 'UA', active: true }]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip(); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-rename-A"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-A"]');
    await input.setValue('');
    await input.trigger('keydown.enter');
    expect(setLabel).toHaveBeenCalledWith('kurogames/wutheringwaves', 'A', '');
  });

  it('commits exactly once when blur follows Enter (D2 guard)', async () => {
    list.mockResolvedValue([{ id: 'A', uid: '700727240', label: '', email: 'a@example.com', username: 'UA', active: true }]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip(); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-rename-A"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-A"]');
    await input.setValue('NewName');
    input.trigger('keydown.enter');
    input.trigger('blur');
    await flushPromises();
    expect(setLabel).toHaveBeenCalledTimes(1);
    expect(setLabel).toHaveBeenLastCalledWith('kurogames/wutheringwaves', 'A', 'NewName');
  });
});
