import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';

const list = vi.fn();
const switchAcc = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGameAccounts: (...a: unknown[]) => list(...a),
  SwitchGameAccount: (...a: unknown[]) => switchAcc(...a),
}));

import AccountChip from '../AccountChip.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {
  account: {
    switchHint: 'Switch account', addInGame: 'Log in a new account in-game',
    switchedToast: 'Switched to {name}; takes effect on next launch',
    gameRunning: 'Close the game before switching accounts',
    switchFailed: 'Account switch failed',
  } } } });

function mountChip() {
  return mount(AccountChip, { props: { gameId: 'kurogames/wutheringwaves' }, global: { plugins: [i18n] } });
}

describe('AccountChip', () => {
  beforeEach(() => { list.mockReset(); switchAcc.mockReset(); });

  it('renders UID primary + email secondary for the active account', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    const w = mountChip();
    await flushPromises();
    expect(w.text()).toContain('700727240');
    expect(w.text()).toContain('a@example.com');
  });

  it('shows the user label as primary, above the uid', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', label: '主帳', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', label: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    const w = mountChip();
    await flushPromises();
    expect(w.text()).toContain('主帳');
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
    expect(w.find('[data-test="account-toast"]').text()).toContain('Switched to');
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
    expect(w.find('[data-test="account-toast"]').text()).toContain('Close the game');
  });
});
