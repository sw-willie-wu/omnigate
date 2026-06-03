<script setup lang="ts"></script>

<template>
  <!-- HeroBase: key-art comes from the global BgLayer; this view only lays the
       readability scrims. No title overlay (per P1 spec). Countdown pill and
       NewsPanel are deferred to P2. BottomBar/HomeActionBar floats at the
       bottom (rendered by App.vue). -->
  <div class="view view-detail">
    <div class="hero-scrim-bottom"></div>
    <div class="hero-scrim-left"></div>
  </div>
</template>
