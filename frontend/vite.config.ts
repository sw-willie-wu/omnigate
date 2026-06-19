import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {execSync} from 'node:child_process'

// App version shown in the UI footbar. Single source of truth = git tag.
// Precedence: OMNIGATE_VERSION env override → nearest public release tag via
// `git describe` (milestone "-mN" tags excluded) → 'dev' when git is
// unavailable. A release build sits exactly on a tag → clean "vX.Y.Z"; a dev
// build gets a "vX.Y.Z-<n>-g<hash>" suffix that flags it as non-release.
function appVersion(): string {
  if (process.env.OMNIGATE_VERSION) return process.env.OMNIGATE_VERSION
  try {
    return execSync('git describe --tags --match "v*" --exclude "*-*"', {
      encoding: 'utf8',
    }).trim()
  } catch {
    return 'dev'
  }
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion()),
  },
})
