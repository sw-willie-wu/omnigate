import { ref, type Ref } from 'vue';

const dialogRef: Ref<any | null> = ref(null);

// 'close' means "dismiss the dialog without making a decision" — used by the
// × button. Callers that want yes/no semantics treat it as not-yes; callers
// that distinguish (e.g. notification queue) keep the item pending.
export type ConfirmResult = 'ok' | 'cancel' | 'close';

export function registerDialog(r: any) {
  dialogRef.value = r;
}

export async function confirm(message: string, ok = 'OK', cancel = 'Cancel'): Promise<ConfirmResult> {
  if (!dialogRef.value) {
    console.error('ConfirmDialog not mounted');
    return 'close';
  }
  return await dialogRef.value.show({ message, ok, cancel });
}
