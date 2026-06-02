import { describe, it, expect, test } from 'vitest';
import en from '../locales/en.json';
import zhTW from '../locales/zh-TW.json';
import zhCN from '../locales/zh-CN.json';

function flatKeys(obj: any, prefix = ''): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (typeof v === 'object' && v !== null) {
      out.push(...flatKeys(v, path));
    } else {
      out.push(path);
    }
  }
  return out;
}

const locales: Record<string, any> = { en, 'zh-TW': zhTW, 'zh-CN': zhCN };

describe('i18n parity', () => {
  it('en + zh-TW + zh-CN have identical key sets', () => {
    const enKeys = flatKeys(en).sort();
    const twKeys = flatKeys(zhTW).sort();
    const cnKeys = flatKeys(zhCN).sort();
    expect(twKeys).toEqual(enKeys);
    expect(cnKeys).toEqual(enKeys);
  });

  it('all update.errors.* error codes present', () => {
    const required = [
      'update.errors.process_blocked',
      'update.errors.manifest_changed',
      'update.errors.manifest_not_found',
      'update.errors.auth_failed',
      'update.errors.predl_stale',
      'update.errors.disk_full',
      'update.errors.cross_volume_temp',
      'update.errors.cross_volume_midrun',
      'update.errors.unsupported_filesystem',
      'update.errors.network',
      'update.errors.corrupt',
      'update.errors.apply_partial',
      'update.errors.unrecoverable',
      'update.errors.internal',
    ];
    const enKeys = new Set(flatKeys(en));
    for (const k of required) {
      expect(enKeys.has(k), `missing en key: ${k}`).toBe(true);
    }
  });

  test('all required keys have non-empty values across all 3 locales', () => {
    const required = [
      // M3.B Stages (9):
      'update.stage.predownloading',
      'update.stage.skipping_download_predl_hit',
      'update.stage.extracting',
      'update.stage.extracting_audio',
      'update.stage.patching',
      'update.stage.verifying_patches',
      'update.stage.applying',
      'update.stage.applying_full',
      'update.stage.cleanup',
      // M3.B Errors (12):
      'update.error.insufficient_space',
      'update.error.cross_volume_setup',
      'update.error.cross_volume_midrun',
      'update.error.unsupported_manifest',
      'update.error.source_corrupted',
      'update.error.source_corrupted_legacy',
      'update.error.source_size_mismatch',
      'update.error.patch_corrupted',
      'update.error.apply_failed',
      'update.error.apply_partial',
      'update.error.permission_denied',
      'update.error.version_unknown',
      // Reason (4):
      'update.reason.version_changed',
      'update.reason.audio_pack_added',
      'update.reason.version_and_audio',
      'update.reason.predownload',
      // Cancel disabled (2):
      'update.cancel_apply_disabled',
      'update.cancel_apply_disabled_eta',
      // Predl size label (1):
      'update.predl_available_size',
      // Bell entries predl_complete (4):
      'update.bell.predl_complete.title',
      'update.bell.predl_complete.body',
      'update.bell.predl_complete.dismiss',
      'update.bell.predl_complete.switch_to',
    ];

    for (const locale of ['en', 'zh-TW', 'zh-CN']) {
      const messages = locales[locale];
      for (const key of required) {
        const value = key.split('.').reduce((o: any, k: string) => o?.[k], messages);
        expect(value, `${locale}.${key} missing`).toBeTruthy();
        expect(typeof value).toBe('string');
        expect((value as string).trim().length).toBeGreaterThan(0);
      }
    }
  });
});
