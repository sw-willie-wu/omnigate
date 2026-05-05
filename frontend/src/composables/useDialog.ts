import { ref, type Ref } from 'vue';

const dialogRef: Ref<any | null> = ref(null);

export function registerDialog(r: any) {
  dialogRef.value = r;
}

export async function confirm(message: string, ok = 'OK', cancel = 'Cancel'): Promise<boolean> {
  if (!dialogRef.value) {
    console.error('ConfirmDialog not mounted');
    return false;
  }
  return await dialogRef.value.show({ message, ok, cancel });
}
