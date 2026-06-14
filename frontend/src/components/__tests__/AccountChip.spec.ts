import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';

const list = vi.fn();
const switchAcc = vi.fn();
const setLabel = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGameAccounts: (...a: unknown[]) => list(...a),
  SwitchGameAccount: (...a: unknown[]) => switchAcc(...a),
  SetAccountLabel: (...a: unknown[]) => setLabel(...a),
}));

const pushToast = vi.fn();
vi.mock('../../composables/useToast', () => ({
  pushToast: (...a: unknown[]) => pushToast(...a),
}));

const gachaReload = vi.fn();
vi.mock('../../stores/gacha', () => ({ useGachaStore: () => ({ reload: gachaReload }) }));

import AccountChip from '../AccountChip.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {
  account: {
    switchHint: 'Switch account', addInGame: 'Log in a new account in-game',
    switchedToast: 'Switched to {name}; takes effect on next launch',
    gameRunning: 'Close the game before switching accounts',
    switchFailed: 'Account switch failed',
    rename: 'Rename', namePlaceholder: 'Custom name', uidPending: 'Available after login',
  } } } });

function mountChip() {
  return mount(AccountChip, { props: { gameId: 'kurogames/wutheringwaves' }, global: { plugins: [i18n], stubs: { teleport: true } } });
}

describe('AccountChip', () => {
  beforeEach(() => { list.mockReset(); switchAcc.mockReset(); setLabel.mockReset(); pushToast.mockReset(); gachaReload.mockReset(); });

  it('renders the email as primary and the UID as secondary', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', label: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    const w = mountChip();
    await flushPromises();
    expect(w.find('.ident .primary').text()).toBe('a@example.com');
    expect(w.find('.ident .secondary').text()).toBe('700727240');
  });

  it('shows the user label as primary, above the account name', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '主帳', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', label: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    const w = mountChip();
    await flushPromises();
    expect(w.find('.ident .primary').text()).toBe('主帳');
  });

  it('shows the login-pending hint as secondary when the UID is unknown', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '', label: '', email: 'a@example.com', username: 'U547195734A', active: true },
    ]);
    const w = mountChip();
    await flushPromises();
    expect(w.find('.ident .secondary').text()).toContain('Available after login');
  });

  it('hides itself when the backend lacks the capability', async () => {
    list.mockRejectedValue(new Error('account switching not supported for this game'));
    const w = mountChip();
    await flushPromises();
    expect(w.find('[data-test="account-chip"]').exists()).toBe(false);
  });

  it('calls SwitchGameAccount when picking another account', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    switchAcc.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-opt-535788351"]').trigger('click');
    expect(switchAcc).toHaveBeenCalledWith('kurogames/wutheringwaves', '535788351');
    await flushPromises();
    expect(pushToast).toHaveBeenCalledWith(expect.stringContaining('Switched to'));
  });

  it('reloads the gacha board after a successful switch', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', label: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    switchAcc.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-opt-535788351"]').trigger('click');
    await flushPromises();
    expect(gachaReload).toHaveBeenCalledWith('kurogames/wutheringwaves');
  });

  it('shows the game-running toast when the switch is blocked', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    switchAcc.mockRejectedValue(new Error('game is running'));
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-opt-535788351"]').trigger('click');
    await flushPromises();
    expect(pushToast).toHaveBeenCalledWith(expect.stringContaining('Close the game'));
  });

  // Test 7: inline rename — clicking ✎ reveals an input; typing + Enter saves.
  it('renames an account via the inline input', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '', email: 'a@example.com', username: 'U547195734A', active: true },
    ]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click'); // open menu
    await w.find('[data-test="account-rename-537195734"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-537195734"]');
    expect(input.exists()).toBe(true);
    await input.setValue('NewName');
    await input.trigger('keydown.enter');
    expect(setLabel).toHaveBeenCalledWith('kurogames/wutheringwaves', '537195734', 'NewName');
  });

  // Test 8: renaming must not switch accounts.
  it('does not switch accounts when renaming', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', label: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-rename-535788351"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-535788351"]');
    await input.setValue('X');
    await input.trigger('keydown.enter');
    expect(switchAcc).not.toHaveBeenCalled();
  });

  // Test 9: an empty input clears the label.
  it('clears the label on empty input', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '主帳', email: 'a@example.com', username: 'U547195734A', active: true },
    ]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-rename-537195734"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-537195734"]');
    await input.setValue('');
    await input.trigger('keydown.enter');
    expect(setLabel).toHaveBeenCalledWith('kurogames/wutheringwaves', '537195734', '');
  });

  // Test 10: commit-once guard (D2) — a blur firing right after Enter must not
  // re-commit with an emptied draft. Fire both before flushing, while the input
  // is still mounted; the synchronous editingId reset makes the blur a no-op.
  it('commits exactly once when blur follows Enter', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '', email: 'a@example.com', username: 'U547195734A', active: true },
    ]);
    setLabel.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-rename-537195734"]').trigger('click');
    const input = w.find('[data-test="account-rename-input-537195734"]');
    await input.setValue('NewName');
    input.trigger('keydown.enter');
    input.trigger('blur');
    await flushPromises();
    expect(setLabel).toHaveBeenCalledTimes(1);
    expect(setLabel).toHaveBeenLastCalledWith('kurogames/wutheringwaves', '537195734', 'NewName');
  });
});
