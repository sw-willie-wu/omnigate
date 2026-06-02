export function formatSize(bytes: number): string {
  const GiB = 1024 * 1024 * 1024
  if (bytes < GiB) {
    return `${Math.round(bytes / (1024 * 1024))} MB`
  }
  return `${(bytes / GiB).toFixed(1)} GB`
}
