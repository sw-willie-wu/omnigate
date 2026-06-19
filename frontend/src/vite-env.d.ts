/// <reference types="vite/client" />

// Injected at build time by vite.config.ts (define) — the app version string.
declare const __APP_VERSION__: string

declare module '*.vue' {
    import type {DefineComponent} from 'vue'
    const component: DefineComponent<{}, {}, any>
    export default component
}
