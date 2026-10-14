<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { money, quantity, dateTime, statusLabels, statusType } from '../utils'
export interface Column { key: string; label: string; type?: string; width?: number; minWidth?: number; hidden?: boolean }
const props = withDefaults(defineProps<{ rows: any[]; columns: Column[]; loading?: boolean; paginate?: boolean; pageSize?: number; empty?: string }>(), { paginate: true, pageSize: 15, empty: '暂无记录，开始录入第一笔业务吧' })
const emit = defineEmits<{ open: [id: number]; row: [row: any] }>()
const page = ref(1)
watch(() => props.rows, () => { page.value = 1 })
const visible = computed(() => props.paginate ? props.rows.slice((page.value - 1) * props.pageSize, page.value * props.pageSize) : props.rows)
const display = (row: any, col: Column) => { const value = row[col.key]; if (value === null || value === undefined || value === '') return '—'; if (col.type === 'money') return money(value); if (col.type === 'qty') return quantity(value); if (col.type === 'datetime') return dateTime(value); if (Array.isArray(value)) return value.join('、'); return value }
</script>
<template>
  <el-table v-loading="loading" :data="visible" style="width:100%" :row-key="(row: any) => row.id || row.key || row.dimension" @row-dblclick="(row: any) => emit('row', row)">
    <template #empty><el-empty :description="empty" :image-size="78" /></template>
    <el-table-column v-for="col in columns.filter(c => !c.hidden)" :key="col.key" :prop="col.key" :label="col.label" :width="col.width" :min-width="col.minWidth || 110" :align="col.type === 'money' || col.type === 'qty' ? 'right' : 'left'" show-overflow-tooltip>
      <template #default="{ row }">
        <el-tag v-if="col.type === 'status'" :type="statusType(row[col.key])" effect="light" size="small">{{ statusLabels[row[col.key]] || row[col.key] }}</el-tag>
        <el-tag v-else-if="col.type === 'boolean'" :type="row[col.key] ? 'success' : 'info'" size="small">{{ row[col.key] ? '启用' : '停用' }}</el-tag>
        <el-button v-else-if="col.type === 'link'" link type="primary" class="table-link" @click="emit('open', row.document_id || row.id)">{{ display(row, col) }}</el-button>
        <span v-else :class="{ 'tabular': col.type === 'money' || col.type === 'qty', 'negative': (col.type === 'money' || col.type === 'qty') && Number(row[col.key]) < 0 }">{{ display(row, col) }}</span>
      </template>
    </el-table-column>
    <el-table-column v-if="$slots.actions" label="操作" fixed="right" :width="150"><template #default="{ row }"><slot name="actions" :row="row" /></template></el-table-column>
  </el-table>
  <div v-if="paginate && rows.length" class="table-footer"><span class="muted">共 {{ rows.length }} 条记录</span><el-pagination v-model:current-page="page" :page-size="pageSize" :total="rows.length" layout="prev, pager, next" background /></div>
</template>
