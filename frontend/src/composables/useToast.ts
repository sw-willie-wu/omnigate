import { ref, type Ref } from 'vue';

const toastRef: Ref<any | null> = ref(null);

export function registerToast(r: any) {
  toastRef.value = r;
}

export function pushToast(message: string, opts: { retryable?: boolean; onRetry?: () => void } = {}) {
  if (!toastRef.value) {
    console.error('ToastHost not mounted');
    return;
  }
  toastRef.value.push({ message, ...opts });
}
