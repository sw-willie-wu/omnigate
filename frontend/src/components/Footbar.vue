<script setup lang="ts">
// Version is injected at build time from the git tag (see vite.config.ts).
// __APP_VERSION__ already carries the leading "v", so it is shown verbatim.
const version = __APP_VERSION__
</script>

<template>
  <div class="footbar">
    <span style="margin-left: auto">{{ version }}</span>
  </div>
</template>
