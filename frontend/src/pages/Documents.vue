<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Plus, Search, Download, Refresh, DocumentChecked } from '@element-plus/icons-vue'
import { useApp } from '../store'
import { api, download, query } from '../api'
import { money, modules, statusLabels } from '../utils'
import DataTable from '../components/DataTable.vue'
const props = defineProps<{ module: string; embedded?: boolean }>()
const app = useApp(), route = useRoute(), kind = ref(''), search = ref(''), status = ref(''), dates = ref<string[]>([]), page = ref(1), total = ref(0), rows = ref<any[]>([]), busy = ref(false)
const kinds = computed(() => app.kinds.filter(k => k.module === props.module))
const currentKind = computed(() => kinds.value.find(k => k.key === kind.value))
const columns = computed(() => [{ key: 'number', label: '单据编号', type: 'link', minWidth: 200 }, { key: 'date', label: '业务日期', width: 108 }, { key: 'partner_name', label: props.module === 'purchase' ? '供应商' : props.module === 'inventory' ? '往来单位' : '客户 / 往来单位', minWidth: 150 }, { key: 'warehouse_name', label: '仓库', minWidth: 115 }, { key: 'amount', label: '单据金额', type: 'money', minWidth: 130, hidden: !app.amountVisible }, { key: 'status', label: '单据状态', type: 'status', width: 100 }, { key: 'creator', label: '制单人', width: 90 }, { key: 'note', label: '备注', minWidth: 160 }])
function filters() { return { kind: kind.value, search: search.value, status: status.value, start: dates.value?.[0], end: dates.value?.[1], page: page.value, page_size: 20 } }
async function load() { if (!kind.value) return; busy.value = true; try { const result = await api('/documents?' + query(filters())); rows.value = result.items; total.value = result.total } catch {} finally { busy.value = false } }
function resetLoad() { page.value = 1; load() }
watch(() => props.module, () => { kind.value = kinds.value[0]?.key || ''; status.value = ''; search.value = ''; resetLoad() }); watch(kind, resetLoad); watch(() => app.tick, load)
onMounted(() => { kind.value = String(route.query.kind || kinds.value[0]?.key || ''); if (!kinds.value.some(k => k.key === kind.value)) kind.value = kinds.value[0]?.key || ''; if (route.query.status) status.value = String(route.query.status); if (route.query.id) app.openDoc(Number(route.query.id)) })
function exportRows() { download('/export?' + query({ type: 'documents', ...filters() })) }
</script>
<template>
  <div v-if="!embedded" class="page-head"><div><h1>{{ modules[module] }}</h1><p>{{ module === 'purchase' ? '从采购订单到入库、退换货，连接供应商与应付账务。' : module === 'sales' ? '统一管理批发、零售与委托代销，跟踪每一笔销售与回款。' : '通过审核单据调整库存与账务，完整保留业务轨迹。' }}</p></div><div class="page-actions"><el-button :icon="Download" @click="exportRows">导出单据</el-button><el-button v-if="app.can(module + '.edit')" type="primary" :icon="Plus" :disabled="!app.writable(module)" @click="app.openDoc(0, kind)">新建{{ currentKind?.name }}</el-button></div></div>
  <section class="panel"><div class="panel-tabs"><el-tabs v-model="kind"><el-tab-pane v-for="item in kinds" :key="item.key" :label="item.name" :name="item.key"/></el-tabs></div><div class="filter-row"><el-input v-model="search" :prefix-icon="Search" placeholder="单据编号、往来单位或备注" clearable @clear="resetLoad" @keyup.enter="resetLoad"/><el-select v-model="status" clearable placeholder="全部状态" @change="resetLoad"><el-option v-for="s in ['draft','submitted','approved','in_progress','completed','cancelled','reversed','closed']" :key="s" :label="statusLabels[s]" :value="s"/></el-select><el-date-picker v-model="dates" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" @change="resetLoad"/><el-button type="primary" plain @click="resetLoad">查询</el-button><div class="spacer"></div><template v-if="embedded"><el-button :icon="Download" @click="exportRows">导出</el-button><el-button v-if="app.can(module + '.edit')" type="primary" :icon="Plus" :disabled="!app.writable(module)" @click="app.openDoc(0, kind)">新建</el-button></template></div>
    <DataTable :rows="rows" :columns="columns" :loading="busy" :paginate="false" :empty="`暂无${currentKind?.name || '业务单据'}，点击新建开始录入`" @open="id => app.openDoc(id)" @row="row => app.openDoc(row.id)"><template #actions="{ row }"><el-button link type="primary" @click="app.openDoc(row.id)">{{ row.status === 'draft' ? '编辑' : row.status === 'submitted' ? '查看 / 审核' : '查看详情' }}</el-button></template></DataTable>
    <div class="table-footer"><span class="muted">共 {{ total }} 张单据 · 金额由服务端计算</span><el-pagination v-model:current-page="page" :total="total" :page-size="20" layout="prev, pager, next" background @current-change="load"/></div>
  </section>
</template>
