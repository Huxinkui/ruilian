<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, shallowRef, watch, nextTick } from 'vue'
import * as echarts from 'echarts'
const props = defineProps<{ option: any; height?: number }>()
const element = ref<HTMLElement>(), chart = shallowRef<echarts.ECharts>()
let observer: ResizeObserver | undefined
const withFont = (option: any) => ({ ...option, textStyle: { ...option.textStyle, fontFamily: 'Noto Sans SC' } })
onMounted(async () => { await document.fonts.load('13px "Noto Sans SC"'); if (!element.value) return; chart.value = echarts.init(element.value); chart.value.setOption(withFont(props.option)); observer = new ResizeObserver(() => chart.value?.resize()); observer.observe(element.value) })
watch(() => props.option, async value => { await nextTick(); chart.value?.setOption(withFont(value), true) }, { deep: true })
onBeforeUnmount(() => { observer?.disconnect(); chart.value?.dispose() })
</script>
<template><div ref="element" :style="{ width: '100%', height: `${height || 280}px` }" role="img" aria-label="经营统计图表"></div></template>
