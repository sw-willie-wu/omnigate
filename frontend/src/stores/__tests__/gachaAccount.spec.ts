import { describe, it, expect, vi, beforeEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const listGachaAccounts = vi.fn();
const addGachaAccountByLogin = vi.fn();
const selectGachaAccount = vi.fn();
const setGachaAccountLabel = vi.fn();
const deleteGachaAccount = vi.fn();

vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGachaAccounts: (...a: unknown[]) => listGachaAccounts(...a),
  AddGachaAccountByLogin: (...a: unknown[]) => addGachaAccountByLogin(...a),
  SelectGachaAccount: (...a: unknown[]) => selectGachaAccount(...a),
  SetGachaAccountLabel: (...a: unknown[]) => setGachaAccountLabel(...a),
  DeleteGachaAccount: (...a: unknown[]) => deleteGachaAccount(...a),
}));

import { useGachaAccountStore } from '../gachaAccount';

const accts = [
  { id: 'ga_A', uid: 'R', label: 'a@b', email: 'a@b', active: true },
  { id: 'ga_B', uid: 'R2', label: 'c@d', email: 'c@d', active: false },
];

describe('gachaAccount store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    listGachaAccounts.mockReset();
    addGachaAccountByLogin.mockReset();
    selectGachaAccount.mockReset();
    setGachaAccountLabel.mockReset();
    deleteGachaAccount.mockReset();
  });

  it('load defaults selectedId to the active account', async () => {
    listGachaAccounts.mockResolvedValue(accts);
    const s = useGachaAccountStore();
    await s.load('hypergryph/endfield');
    expect(s.accountsFor('hypergryph/endfield').length).toBe(2);
    expect(s.selectedFor('hypergryph/endfield')?.id).toBe('ga_A');
  });

  it('select changes selection and calls backend', async () => {
    listGachaAccounts.mockResolvedValue(accts);
    selectGachaAccount.mockResolvedValue(undefined);
    const s = useGachaAccountStore();
    await s.load('hypergryph/endfield');
    await s.select('hypergryph/endfield', 'ga_B');
    expect(s.selectedFor('hypergryph/endfield')?.id).toBe('ga_B');
    expect(selectGachaAccount).toHaveBeenCalledWith('hypergryph/endfield', 'ga_B');
  });

  it('explicit selection survives a reload when the account is still present', async () => {
    listGachaAccounts.mockResolvedValue(accts);
    const s = useGachaAccountStore();
    await s.load('hypergryph/endfield');
    await s.select('hypergryph/endfield', 'ga_B');
    await s.load('hypergryph/endfield');
    expect(s.selectedFor('hypergryph/endfield')?.id).toBe('ga_B');
  });

  it('addByLogin reloads and selects the new account', async () => {
    listGachaAccounts.mockResolvedValue(accts);
    const newAcc = { id: 'ga_C', uid: 'R3', label: 'e@f', email: 'e@f', active: false };
    addGachaAccountByLogin.mockResolvedValue(newAcc);
    const s = useGachaAccountStore();
    await s.load('hypergryph/endfield');
    const acc = await s.addByLogin('hypergryph/endfield', 'e@f', 'pass');
    expect(acc.id).toBe('ga_C');
    // after reload, the new account is selected
    expect(s.byGid['hypergryph/endfield']?.selectedId).toBe('ga_C');
  });

  it('load on backend error sets loaded=true with empty accounts', async () => {
    listGachaAccounts.mockRejectedValue(new Error('not supported'));
    const s = useGachaAccountStore();
    await s.load('hypergryph/endfield');
    expect(s.accountsFor('hypergryph/endfield').length).toBe(0);
    expect(s.byGid['hypergryph/endfield']?.loaded).toBe(true);
  });

  it('reset clears all gid state', async () => {
    listGachaAccounts.mockResolvedValue(accts);
    const s = useGachaAccountStore();
    await s.load('hypergryph/endfield');
    s.reset();
    expect(s.accountsFor('hypergryph/endfield').length).toBe(0);
  });
});
