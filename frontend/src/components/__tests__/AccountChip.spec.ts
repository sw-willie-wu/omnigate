import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';

const list = vi.fn();
const setLabel = vi.fn();
const kind = vi.fn();
const gachaList = vi.fn();
const gachaSelect = vi.fn();
const gachaSetLabel = vi.fn();
const gachaRemove = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGameAccounts: (...a: unknown[]) => list(...a),
  SetAccountLabel: (...a: unknown[]) => setLabel(...a),
  GameAccountKind: (...a: unknown[]) => kind(...a),
  ListGachaAccounts: (...a: unknown[]) => gachaList(...a),
  SelectGachaAccount: (...a: unknown[]) => gachaSelect(...a),
  SetGachaAccountLabel: (...a: unknown[]) => gachaSetLabel(...a),
  DeleteGachaAccount: (...a: unknown[]) => gachaRemove(...a),
  AddGachaAccountByLogin: vi.fn(),
}));

// Stub LoginModal so we don't pull in its wails imports / real markup; assert by
// the host's data-test wrapper instead.
vi.mock('../LoginModal.vue', () => ({
  default: {
    name: 'LoginModal',
    props: ['gameId'],
    emits: ['added', 'close'],
    template: '<div data-test="login-modal-stub"></div>',
  },
}));

import AccountChip from '../AccountChip.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {
  account: {
    switchHint: 'Switch account',
    rename: 'Rename', namePlaceholder: 'Custom name',
    uidPending: 'Available after login', currentlyLoggedIn: 'Logged in',
    addAccount: '+ Add account', delete: 'Remove account',
  } } } });

function mountChip(gameId = 'kurogames/wutheringwaves') {
  setActivePinia(createPinia());
  return mount(AccountChip, { props: { gameId }, global: { plugins: [i18n], stubs: { teleport: true } } });
}

describe('AccountChip — switcher (WuWa) path', () => {
  beforeEach(() => {
    list.mockReset(); setLabel.mockReset();
    // switcher games: GameAccountKind resolves to 'switcher'
    kind.mockReset(); kind.mockResolvedValue('switcher');
    gachaList.mockReset(); gachaList.mockResolvedValue([]);
    gachaSelect.mockReset(); gachaSetLabel.mockReset(); gachaRemove.mockReset();
  });

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

  it('has NO add-account footer on the switcher path', async () => {
    list.mockResolvedValue([{ id: 'A', uid: '700727240', label: '', email: 'a@example.com', username: 'UA', active: true }]);
    const w = mountChip(); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    expect(w.find('[data-test="account-add"]').exists()).toBe(false);
    expect(w.find('[data-test="account-del-A"]').exists()).toBe(false);
  });
});

describe('AccountChip — credential (Endfield gacha) path', () => {
  beforeEach(() => {
    list.mockReset(); list.mockResolvedValue([]);
    setLabel.mockReset();
    kind.mockReset(); kind.mockResolvedValue('credential');
    gachaList.mockReset();
    gachaSelect.mockReset(); gachaSelect.mockResolvedValue(undefined);
    gachaSetLabel.mockReset(); gachaSetLabel.mockResolvedValue(undefined);
    gachaRemove.mockReset(); gachaRemove.mockResolvedValue(undefined);
  });

  it('renders the chip even with 0 accounts (supported=true for credential)', async () => {
    gachaList.mockResolvedValue([]);
    const w = mountChip('gryphline/endfield'); await flushPromises();
    expect(w.find('[data-test="account-chip"]').exists()).toBe(true);
  });

  it('clicking the empty chip opens the LoginModal (no dropdown)', async () => {
    gachaList.mockResolvedValue([]);
    const w = mountChip('gryphline/endfield'); await flushPromises();
    expect(w.find('[data-test="login-modal-stub"]').exists()).toBe(false);
    await w.find('[data-test="account-chip"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-test="login-modal-stub"]').exists()).toBe(true);
    expect(w.find('[data-test="account-add"]').exists()).toBe(false); // dropdown not opened
  });

  it('dropdown shows a + Add account row that opens the modal', async () => {
    gachaList.mockResolvedValue([
      { id: 'G1', uid: '1001', label: '', email: 'g1@example.com', active: true },
    ]);
    const w = mountChip('gryphline/endfield'); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    const add = w.find('[data-test="account-add"]');
    expect(add.exists()).toBe(true);
    await add.trigger('click');
    await flushPromises();
    expect(w.find('[data-test="login-modal-stub"]').exists()).toBe(true);
  });

  it('selecting an account calls gachaAccount.select via SelectGachaAccount', async () => {
    gachaList.mockResolvedValue([
      { id: 'G1', uid: '1001', label: '', email: 'g1@example.com', active: true },
      { id: 'G2', uid: '1002', label: '', email: 'g2@example.com', active: false },
    ]);
    const w = mountChip('gryphline/endfield'); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-opt-G2"]').trigger('click');
    await flushPromises();
    expect(gachaSelect).toHaveBeenCalledWith('gryphline/endfield', 'G2');
    expect(w.find('.ident .primary').text()).toBe('g2@example.com');
  });

  it('delete affordance calls DeleteGachaAccount', async () => {
    gachaList.mockResolvedValue([
      { id: 'G1', uid: '1001', label: '', email: 'g1@example.com', active: true },
    ]);
    const w = mountChip('gryphline/endfield'); await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    const del = w.find('[data-test="account-del-G1"]');
    expect(del.exists()).toBe(true);
    await del.trigger('click');
    await flushPromises();
    expect(gachaRemove).toHaveBeenCalledWith('gryphline/endfield', 'G1');
  });

  it('uses the gacha store, not the switcher account store', async () => {
    gachaList.mockResolvedValue([{ id: 'G1', uid: '1001', label: '', email: 'g1@example.com', active: true }]);
    const w = mountChip('gryphline/endfield'); await flushPromises();
    expect(w.find('.ident .primary').text()).toBe('g1@example.com');
    expect(list).not.toHaveBeenCalled();
  });
});
