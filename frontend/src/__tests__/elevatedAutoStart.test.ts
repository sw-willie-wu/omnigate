import { describe, it, expect, vi } from 'vitest';
import { runElevatedAutoStart } from '../composables/elevatedAutoStart';

describe('runElevatedAutoStart', () => {
  it('selects and starts the pending game', async () => {
    const select = vi.fn();
    const startUpdate = vi.fn().mockResolvedValue(undefined);
    await runElevatedAutoStart(async () => 'kurogames/wutheringwaves', select, startUpdate);
    expect(select).toHaveBeenCalledWith('kurogames/wutheringwaves');
    expect(startUpdate).toHaveBeenCalledWith('kurogames/wutheringwaves');
  });

  it('does nothing when there is no pending game', async () => {
    const select = vi.fn();
    const startUpdate = vi.fn();
    await runElevatedAutoStart(async () => '', select, startUpdate);
    expect(select).not.toHaveBeenCalled();
    expect(startUpdate).not.toHaveBeenCalled();
  });

  it('swallows errors (never rejects)', async () => {
    const startUpdate = vi.fn().mockRejectedValue(new Error('boom'));
    await expect(
      runElevatedAutoStart(async () => 'g', vi.fn(), startUpdate),
    ).resolves.toBeUndefined();
  });
});
