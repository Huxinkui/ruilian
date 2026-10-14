<script setup lang="ts">
import { useApp } from '../store'
defineProps<{ logo?: string }>()
const app = useApp()
</script>
<template><img class="brand-mark" :src="(logo === undefined ? app.branding.brand_logo : logo) || '/brand-default.png'" alt="软件 Logo"/></template>
