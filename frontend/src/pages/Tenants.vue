<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  OfficeBuilding,
  Plus,
  Refresh,
  Setting,
  Lock,
  Search,
  CopyDocument,
  CircleCheck,
  Folder,
  Key,
  DataAnalysis,
  Switch,
  Warning,
  Edit,
  ArrowRight,
  Monitor,
  Coin,
  Cpu
} from '@element-plus/icons-vue'
import { api } from '../api'
import { useApp } from '../store'
import { dateTime } from '../utils'

const app = useApp()
const router = useRouter()
const busy = ref(false)
const saving = ref(false)

// 筛选与搜索
const search = ref('')
const statusFilter = ref('')
const storageFilter = ref('')

// 数据状态
const tenants = ref<any[]>([])
const tenantStats = ref<any>({ total: 0, total_used_mb: 0, total_quota_mb: 0 })

// 弹窗控制
const tenantOpen = ref(false)
const editOpen = ref(false)
const storageOpen = ref(false)
const pwdOpen = ref(false)

// 新建租户表单
const tenantForm = reactive({
  code: '',
  name: '',
  max_storage_mb: 500,
  admin_password: 'qq13608472109',
  note: ''
})

// 编辑租户表单
const editForm = reactive({
  code: '',
  name: '',
  max_storage_mb: 500,
  note: ''
})

// 配额调整表单
const storageForm = reactive({
  code: '',
  name: '',
  max_storage_mb: 500
})

// 密码重置表单
const pwdForm = reactive({
  code: '',
  name: '',
  password: 'qq13608472109'
})

// 快捷配额预设
const quotaPresets = [
  { label: '200 MB', value: 200 },
  { label: '500 MB (标准)', value: 500 },
  { label: '1024 MB (1 GB)', value: 1024 },
  { label: '2048 MB (2 GB)', value: 2048 },
  { label: '不限容量', value: 0 }
]

async function load() {
  busy.value = true
  try {
    const res = await api('/system/tenants')
    tenants.value = res.items || res.tenants || []
    tenantStats.value = {
      total: res.total || tenants.value.length,
      total_used_mb: res.total_used_mb || 0,
      total_quota_mb: res.total_quota_mb || 0
    }
    // 同步给全局 store，供顶部租户切换选择器使用
    app.tenantList = tenants.value
  } catch (err: any) {
    ElMessage.error(err?.message || '加载租户列表失败')
  } finally {
    busy.value = false
  }
}

onMounted(load)
watch(() => app.tick, load)

// 活跃与告警统计
const activeCount = computed(() => tenants.value.filter(t => t.status === 'active').length)
const disabledCount = computed(() => tenants.value.filter(t => t.status === 'disabled').length)
const warningTenants = computed(() => {
  return tenants.value.filter(t => {
    if (!t.max_storage_mb || t.max_storage_mb <= 0) return false
    const used = t.used_storage_mb || t.used_mb || 0
    return (used / t.max_storage_mb) >= 0.8
  })
})

// 综合使用率
const overallUsagePercent = computed(() => {
  if (!tenantStats.value.total_quota_mb || tenantStats.value.total_quota_mb <= 0) return 0
  const pct = Math.round((tenantStats.value.total_used_mb / tenantStats.value.total_quota_mb) * 100)
  return Math.min(100, Math.max(0, pct))
})

// 过滤后的列表
const filteredTenants = computed(() => {
  return tenants.value.filter(t => {
    // 关键字搜索
    const q = search.value.toLowerCase().trim()
    const matchSearch = !q ||
      t.code.toLowerCase().includes(q) ||
      t.name.toLowerCase().includes(q) ||
      (t.note && t.note.toLowerCase().includes(q))

    // 状态过滤
    const matchStatus = !statusFilter.value || t.status === statusFilter.value

    // 配额状态过滤
    let matchStorage = true
    if (storageFilter.value === 'warning') {
      const used = t.used_storage_mb || t.used_mb || 0
      matchStorage = t.max_storage_mb > 0 && (used / t.max_storage_mb) >= 0.8 && (used / t.max_storage_mb) < 1.0
    } else if (storageFilter.value === 'exceeded') {
      const used = t.used_storage_mb || t.used_mb || 0
      matchStorage = t.max_storage_mb > 0 && used >= t.max_storage_mb
    } else if (storageFilter.value === 'normal') {
      const used = t.used_storage_mb || t.used_mb || 0
      matchStorage = !t.max_storage_mb || t.max_storage_mb <= 0 || (used / t.max_storage_mb) < 0.8
    }

    return matchSearch && matchStatus && matchStorage
  })
})

function createTenant() {
  Object.assign(tenantForm, {
    code: '',
    name: '',
    max_storage_mb: 500,
    admin_password: 'qq13608472109',
    note: ''
  })
  tenantOpen.value = true
}

async function saveTenant() {
  const code = tenantForm.code.trim()
  const name = tenantForm.name.trim()
  if (!code || !name) {
    ElMessage.warning('请填写租户代码与组织名称')
    return
  }
  if (!/^[a-zA-Z0-9_-]{1,32}$/.test(code)) {
    ElMessage.warning('租户代码仅支持 1-32 位字母、数字、下划线及连字符')
    return
  }
  saving.value = true
  try {
    await api('/system/tenants', 'POST', tenantForm)
    ElMessage.success(`租户空间 [${name}] 创建成功`)
    tenantOpen.value = false
    await load()
  } catch (err: any) {
    ElMessage.error(err?.message || '创建租户失败')
  } finally {
    saving.value = false
  }
}

function openEdit(row: any) {
  Object.assign(editForm, {
    code: row.code,
    name: row.name,
    max_storage_mb: row.max_storage_mb,
    note: row.note || ''
  })
  editOpen.value = true
}

async function saveEdit() {
  if (!editForm.name.trim()) {
    ElMessage.warning('请填写租户组织名称')
    return
  }
  saving.value = true
  try {
    await api(`/system/tenants/${editForm.code}`, 'PUT', {
      name: editForm.name,
      note: editForm.note,
      max_storage_mb: editForm.max_storage_mb
    })
    ElMessage.success(`租户 [${editForm.name}] 信息与配额已更新`)
    editOpen.value = false
    await load()
  } catch (err: any) {
    ElMessage.error(err?.message || '更新租户信息失败')
  } finally {
    saving.value = false
  }
}

function editStorage(row: any) {
  Object.assign(storageForm, {
    code: row.code,
    name: row.name,
    max_storage_mb: row.max_storage_mb
  })
  storageOpen.value = true
}

async function saveStorage() {
  saving.value = true
  try {
    await api(`/system/tenants/${storageForm.code}/storage`, 'PUT', {
      max_storage_mb: storageForm.max_storage_mb
    })
    ElMessage.success(`租户 [${storageForm.name}] 空间配额已更新为 ${storageForm.max_storage_mb ? storageForm.max_storage_mb + ' MB' : '不限容量'}`)
    storageOpen.value = false
    await load()
  } catch (err: any) {
    ElMessage.error(err?.message || '调整配额失败')
  } finally {
    saving.value = false
  }
}

function resetTenantPwd(row: any) {
  Object.assign(pwdForm, {
    code: row.code,
    name: row.name,
    password: 'qq13608472109'
  })
  pwdOpen.value = true
}

async function saveTenantPwd() {
  if (pwdForm.password.length < 8) {
    ElMessage.warning('密码长度至少需要 8 位')
    return
  }
  saving.value = true
  try {
    await api(`/system/tenants/${pwdForm.code}/reset-password`, 'POST', {
      password: pwdForm.password
    })
    ElMessage.success(`租户 [${pwdForm.name}] 管理员密码已重置成功`)
    pwdOpen.value = false
  } catch (err: any) {
    ElMessage.error(err?.message || '重置密码失败')
  } finally {
    saving.value = false
  }
}

async function toggleTenantStatus(row: any) {
  const newStatus = row.status === 'active' ? 'disabled' : 'active'
  const action = newStatus === 'active' ? '启用' : '停用'
  await ElMessageBox.confirm(
    `确认${action}租户空间【${row.name}】？${newStatus === 'disabled' ? '停用后该租户下所有用户将禁止登录与业务操作。' : '启用后租户用户可正常登录与开展业务。'}`,
    '提示',
    {
      type: newStatus === 'disabled' ? 'warning' : 'info',
      confirmButtonText: `确定${action}`
    }
  )
  saving.value = true
  try {
    await api(`/system/tenants/${row.code}/status`, 'POST', { status: newStatus })
    ElMessage.success(`租户空间已${action}`)
    await load()
  } catch (err: any) {
    ElMessage.error(err?.message || `${action}失败`)
  } finally {
    saving.value = false
  }
}

async function copyCode(code: string) {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(code)
    } else {
      const ta = document.createElement('textarea')
      ta.value = code
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    ElMessage.success(`已复制租户代码: ${code}`)
  } catch {
    ElMessage.info(`租户代码: ${code}`)
  }
}

async function enterTenant(row: any) {
  try {
    await app.switchTenant(row.code)
    ElMessage.success(`已切换至租户空间【${row.name}】，可查看和修改该租户的所有进销存与账务资源`)
    await router.push('/')
  } catch (err: any) {
    ElMessage.error(err?.message || '切换租户空间失败')
  }
}
</script>

<template>
  <div v-loading="busy" class="tenants-page">
    <!-- 顶部主标题与操作栏 -->
    <div class="page-head">
      <div class="head-info">
        <div class="head-badge">
          <span class="pulse-dot"></span>
          <span>SaaS 控制台 · Database-per-Tenant 物理级隔离</span>
        </div>
        <h1>多租户空间与资源配额中心</h1>
        <p class="head-desc">
          平台超级管理员（huxinkui）集中管控所有租户空间、存储配额及生命周期；各租户在专属独立物理数据库中自闭环运行 Ruilian ERP。
        </p>
      </div>
      <div class="page-actions">
        <el-button
          v-if="app.tenant?.code && app.tenant?.code !== 'default'"
          type="info"
          plain
          @click="app.switchTenant('default')"
        >
          返回平台控制台
        </el-button>
        <el-button :icon="Refresh" @click="load">刷新列表</el-button>
        <el-button type="primary" :icon="Plus" class="btn-create" @click="createTenant">
          新建租户空间
        </el-button>
      </div>
    </div>

    <!-- 平台架构核心规范卡片 -->
    <div class="arch-card">
      <div class="arch-col">
        <div class="arch-icon blue"><el-icon><Monitor /></el-icon></div>
        <div class="arch-text">
          <b>后台管理员与专属租户</b>
          <p>huxinkui 仅供系统后台超级管理员登录与全局管控；各租户只能使用专属用户ID登录，禁止以管理员账号登录。</p>
        </div>
      </div>
      <div class="arch-divider"></div>
      <div class="arch-col">
        <div class="arch-icon green"><el-icon><OfficeBuilding /></el-icon></div>
        <div class="arch-text">
          <b>租户资源自主管理</b>
          <p>各租户使用独立的 Ruilian 系统，自主管理商品、采购、销售、库存与账务。</p>
        </div>
      </div>
      <div class="arch-divider"></div>
      <div class="arch-col">
        <div class="arch-icon violet"><el-icon><Folder /></el-icon></div>
        <div class="arch-text">
          <b>物理数据库隔离</b>
          <p>Database-per-Tenant 架构，各租户拥有专属 SQLite 物理文件，数据绝对隔离。</p>
        </div>
      </div>
      <div class="arch-divider"></div>
      <div class="arch-col">
        <div class="arch-icon orange"><el-icon><Coin /></el-icon></div>
        <div class="arch-text">
          <b>动态容量配额</b>
          <p>随时按需调整各租户空间配额（如 500MB、1GB、不限），超限自动保护拦截。</p>
        </div>
      </div>
    </div>

    <!-- 空间指标大盘 -->
    <div class="metric-grid">
      <div class="metric-card">
        <div class="metric-label">
          <span>租户空间总数</span>
          <div class="metric-icon blue"><el-icon><OfficeBuilding /></el-icon></div>
        </div>
        <div class="metric-value">
          {{ tenantStats.total }}
          <small class="unit">个</small>
        </div>
        <div class="metric-footer">
          <span>
            运行中 <b>{{ activeCount }}</b> · 停用 <b>{{ disabledCount }}</b>
          </span>
          <el-tag size="small" type="success" effect="plain">100% 独立环境</el-tag>
        </div>
      </div>

      <div class="metric-card">
        <div class="metric-label">
          <span>物理磁盘总占用</span>
          <div class="metric-icon violet"><el-icon><Folder /></el-icon></div>
        </div>
        <div class="metric-value">
          {{ (tenantStats.total_used_mb || 0).toFixed(2) }}
          <small class="unit">MB</small>
        </div>
        <div class="metric-footer">
          <span>含 DB、WAL 及 SHM 物理文件</span>
          <b class="status-tip">实时探测</b>
        </div>
      </div>

      <div class="metric-card">
        <div class="metric-label">
          <span>平台分配总配额</span>
          <div class="metric-icon orange"><el-icon><DataAnalysis /></el-icon></div>
        </div>
        <div class="metric-value">
          {{ tenantStats.total_quota_mb ? tenantStats.total_quota_mb : '不限' }}
          <small v-if="tenantStats.total_quota_mb" class="unit">MB</small>
        </div>
        <div class="metric-footer">
          <span>所有租户存储上限总和</span>
          <b class="status-tip">配额受控</b>
        </div>
      </div>

      <div class="metric-card">
        <div class="metric-label">
          <span>综合配额使用率</span>
          <div class="metric-icon green"><el-icon><CircleCheck /></el-icon></div>
        </div>
        <div class="metric-value">
          {{ overallUsagePercent }}
          <small class="unit">%</small>
        </div>
        <div class="metric-footer">
          <div style="flex: 1; margin-right: 12px;">
            <el-progress
              :percentage="overallUsagePercent"
              :stroke-width="6"
              :show-text="false"
              :status="overallUsagePercent > 90 ? 'exception' : (overallUsagePercent > 70 ? 'warning' : 'success')"
            />
          </div>
          <el-tag size="small" :type="overallUsagePercent > 85 ? 'warning' : 'success'">
            {{ overallUsagePercent > 85 ? '需扩容' : '配额充裕' }}
          </el-tag>
        </div>
      </div>
    </div>

    <!-- 空间告警横幅（如果有租户使用率超过 80%） -->
    <el-alert
      v-if="warningTenants.length"
      type="warning"
      show-icon
      :closable="false"
      class="alert-warning-banner"
    >
      <template #title>
        <b>存储空间使用预警（{{ warningTenants.length }} 个租户接近或达到配额上限）</b>
      </template>
      <template #default>
        检测到租户【{{ warningTenants.map(t => t.name).join('、') }}】存储使用率已达 80% 以上。达到 100% 后系统将自动限制单据写入。建议管理员及时点击对应租户的【配额】按钮进行扩容。
      </template>
    </el-alert>

    <!-- 租户列表主面板 -->
    <section class="panel">
      <div class="panel-head">
        <div class="panel-title-area">
          <h3>租户空间列表</h3>
          <p>共 {{ tenants.length }} 个独立租户空间，支持一键切换跨空间管理、存储配额动态调整与管理员密码重置</p>
        </div>
        <div class="filter-actions">
          <el-input
            v-model="search"
            :prefix-icon="Search"
            placeholder="搜索租户代码、名称或备注..."
            clearable
            style="width: 250px"
          />
          <el-select v-model="statusFilter" placeholder="运行状态" clearable style="width: 120px">
            <el-option label="全部状态" value="" />
            <el-option label="正常运行" value="active" />
            <el-option label="已停用" value="disabled" />
          </el-select>
          <el-select v-model="storageFilter" placeholder="配额健康" clearable style="width: 130px">
            <el-option label="全部配额" value="" />
            <el-option label="正常 (<80%)" value="normal" />
            <el-option label="告警 (≥80%)" value="warning" />
            <el-option label="超限 (≥100%)" value="exceeded" />
          </el-select>
        </div>
      </div>

      <el-table :data="filteredTenants" style="width: 100%" class="tenant-table" stripe>
        <!-- 租户代码与专属用户ID -->
        <el-table-column prop="code" label="租户代码 / 专属用户ID" min-width="190">
          <template #default="{ row }">
            <div class="code-cell">
              <span class="code-badge">{{ row.code }}</span>
              <el-button link :icon="CopyDocument" @click="copyCode(row.code)" title="复制租户代号" />
              <el-tag v-if="row.code === 'default'" size="small" type="info" effect="dark" class="ml4">主空间</el-tag>
            </div>
            <div class="tenant-user-tag">
              <el-icon><User /></el-icon> 租户用户ID: <code>{{ row.admin_user || row.code }}</code>
              <el-tag size="small" type="success" effect="plain" style="margin-left: 4px;">专属登录账号</el-tag>
            </div>
          </template>
        </el-table-column>

        <!-- 租户名称与备注 -->
        <el-table-column prop="name" label="租户企业 / 组织名称" min-width="180">
          <template #default="{ row }">
            <div class="tenant-name-wrap">
              <b class="tenant-name">{{ row.name }}</b>
              <div v-if="row.note" class="muted text-small note-text" :title="row.note">{{ row.note }}</div>
            </div>
          </template>
        </el-table-column>

        <!-- 存储配额与使用情况 -->
        <el-table-column label="存储空间与配额上限" min-width="240">
          <template #default="{ row }">
            <div class="storage-column">
              <div class="storage-text-row">
                <span class="storage-used">{{ (row.used_storage_mb || row.used_mb || 0).toFixed(2) }} MB</span>
                <span class="storage-sep">/</span>
                <span v-if="row.max_storage_mb > 0" class="storage-max">{{ row.max_storage_mb }} MB</span>
                <el-tag v-else size="small" type="info" effect="plain">不限容量</el-tag>
                <span v-if="row.max_storage_mb > 0" class="storage-pct">
                  ({{ Math.min(100, Math.round(((row.used_storage_mb || row.used_mb || 0) / row.max_storage_mb) * 100)) }}%)
                </span>
              </div>
              <div v-if="row.max_storage_mb > 0" class="progress-wrap">
                <el-progress
                  :percentage="Math.min(100, Math.round(((row.used_storage_mb || row.used_mb || 0) / row.max_storage_mb) * 100))"
                  :status="((row.used_storage_mb || row.used_mb || 0) >= row.max_storage_mb) ? 'exception' : (((row.used_storage_mb || row.used_mb || 0) / row.max_storage_mb) >= 0.8 ? 'warning' : 'success')"
                  :stroke-width="7"
                />
              </div>
              <div v-if="row.max_storage_mb > 0 && (row.used_storage_mb || row.used_mb || 0) >= row.max_storage_mb" class="exceeded-badge">
                <el-icon><Warning /></el-icon> 空间超限，已限制写入
              </div>
            </div>
          </template>
        </el-table-column>

        <!-- 物理数据库隔离路径 -->
        <el-table-column label="物理隔离数据库" min-width="210">
          <template #default="{ row }">
            <div class="db-info-cell">
              <code class="db-path">{{ row.code === 'default' ? 'data/ruilian.db' : `data/tenants/${row.code}/ruilian.db` }}</code>
              <div class="db-tags">
                <span class="db-tag">SQLite 3</span>
                <span class="db-tag">WAL 独立事务</span>
              </div>
            </div>
          </template>
        </el-table-column>

        <!-- 运行状态 -->
        <el-table-column label="运行状态" width="105">
          <template #default="{ row }">
            <div class="status-cell">
              <span class="status-dot" :class="row.status === 'active' ? 'active' : 'disabled'"></span>
              <span :class="row.status === 'active' ? 'text-success' : 'text-danger'">
                {{ row.status === 'active' ? '正常运行' : '已停用' }}
              </span>
            </div>
          </template>
        </el-table-column>

        <!-- 创建时间 -->
        <el-table-column label="创建时间" min-width="155">
          <template #default="{ row }">
            <span class="muted text-small">{{ dateTime(row.created_at) }}</span>
          </template>
        </el-table-column>

        <!-- 操作栏 -->
        <el-table-column label="管控操作" width="310" fixed="right">
          <template #default="{ row }">
            <div class="action-buttons">
              <el-tag
                v-if="row.code === (app.tenant?.code || 'default')"
                type="success"
                size="small"
                effect="dark"
                class="current-tag"
              >
                当前管理中
              </el-tag>
              <el-button
                v-else
                link
                type="success"
                :icon="Switch"
                class="btn-enter"
                @click="enterTenant(row)"
              >
                进入管理数据
              </el-button>

              <el-button link type="primary" :icon="Setting" @click="editStorage(row)">
                配额
              </el-button>

              <el-button link type="primary" :icon="Edit" @click="openEdit(row)">
                编辑
              </el-button>

              <el-button link type="primary" :icon="Key" @click="resetTenantPwd(row)">
                重置密码
              </el-button>

              <el-button
                v-if="row.code !== 'default'"
                link
                :type="row.status === 'active' ? 'danger' : 'success'"
                @click="toggleTenantStatus(row)"
              >
                {{ row.status === 'active' ? '停用' : '启用' }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <!-- 新建租户空间弹窗 -->
    <el-dialog
      v-model="tenantOpen"
      title="新建租户空间（分配独立数据库与配额）"
      width="600px"
      destroy-on-close
      class="custom-dialog"
    >
      <el-form label-position="top">
        <div class="form-grid two">
          <el-form-item label="租户代码 (唯一标识) *" class="wide">
            <el-input
              v-model="tenantForm.code"
              placeholder="如：shanghai / branch01"
              maxlength="32"
              clearable
            />
            <small class="muted">登录时填写的空间代号，仅支持字母、数字、下划线及连字符（创建后不可更改）</small>
          </el-form-item>

          <el-form-item label="租户企业 / 组织名称 *" class="wide">
            <el-input
              v-model="tenantForm.name"
              placeholder="如：华东运营中心 / 上海分公司"
              maxlength="80"
              clearable
            />
          </el-form-item>

          <el-form-item label="存储空间配额上限 (MB) *" class="wide">
            <el-input-number
              v-model="tenantForm.max_storage_mb"
              :min="0"
              :max="1048576"
              :precision="0"
              controls-position="right"
              style="width: 100%"
            />
            <div class="quota-chips">
              <span class="chips-label">快捷预设:</span>
              <el-button
                v-for="p in quotaPresets"
                :key="p.value"
                size="small"
                :type="tenantForm.max_storage_mb === p.value ? 'primary' : 'default'"
                @click="tenantForm.max_storage_mb = p.value"
              >
                {{ p.label }}
              </el-button>
            </div>
            <small class="muted">填 0 表示不限制容量；超出配额时系统将自动限制单据写入</small>
          </el-form-item>

          <el-form-item label="租户管理员初始密码 *" class="wide">
            <el-input
              v-model="tenantForm.admin_password"
              type="password"
              show-password
              placeholder="默认 qq13608472109"
            />
            <small class="muted">
              该租户专属登录用户ID为 <code>{{ tenantForm.code.trim() || '租户代码' }}</code>。租户不可使用管理员账号 (huxinkui) 登录，huxinkui 仅供系统超级管理员登录平台。
            </small>
          </el-form-item>

          <el-form-item label="备注说明" class="wide">
            <el-input
              v-model="tenantForm.note"
              type="textarea"
              :rows="2"
              placeholder="可选租户备注信息，如所属区域、业务联系人等"
            />
          </el-form-item>
        </div>

        <div class="db-preview-card">
          <el-icon class="preview-icon"><Folder /></el-icon>
          <div>
            <b>物理隔离路径预览：</b>
            <code>data/tenants/{{ tenantForm.code.trim() || '&lt;租户代码&gt;' }}/ruilian.db</code>
            <p>基于 Database-per-Tenant 架构，创建后将分配专属物理 SQLite 数据库，数据互不交叉。</p>
          </div>
        </div>
      </el-form>

      <template #footer>
        <el-button @click="tenantOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveTenant">立即创建租户空间</el-button>
      </template>
    </el-dialog>

    <!-- 编辑租户信息弹窗 -->
    <el-dialog v-model="editOpen" title="编辑租户信息与空间配额" width="520px">
      <el-form label-position="top">
        <el-form-item label="租户代码">
          <el-input :model-value="editForm.code" disabled />
          <small class="muted">租户唯一代号不可变更</small>
        </el-form-item>

        <el-form-item label="租户企业 / 组织名称 *">
          <el-input v-model="editForm.name" maxlength="80" clearable />
        </el-form-item>

        <el-form-item label="存储空间配额上限 (MB)">
          <el-input-number
            v-model="editForm.max_storage_mb"
            :min="0"
            :max="1048576"
            :precision="0"
            controls-position="right"
            style="width: 100%"
          />
          <div class="quota-chips">
            <span class="chips-label">快捷预设:</span>
            <el-button
              v-for="p in quotaPresets"
              :key="p.value"
              size="small"
              :type="editForm.max_storage_mb === p.value ? 'primary' : 'default'"
              @click="editForm.max_storage_mb = p.value"
            >
              {{ p.label }}
            </el-button>
          </div>
        </el-form-item>

        <el-form-item label="备注说明">
          <el-input v-model="editForm.note" type="textarea" :rows="2" placeholder="可选备注说明" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="editOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveEdit">保存修改</el-button>
      </template>
    </el-dialog>

    <!-- 快速配额调整弹窗 -->
    <el-dialog v-model="storageOpen" title="调整租户存储空间配额" width="480px">
      <el-form label-position="top">
        <el-form-item label="租户空间">
          <el-input :model-value="`${storageForm.name} (${storageForm.code})`" disabled />
        </el-form-item>

        <el-form-item label="最大存储上限 (MB)">
          <el-input-number
            v-model="storageForm.max_storage_mb"
            :min="0"
            :max="1048576"
            :precision="0"
            controls-position="right"
            style="width: 100%"
          />
          <div class="quota-chips">
            <span class="chips-label">快捷预设:</span>
            <el-button
              v-for="p in quotaPresets"
              :key="p.value"
              size="small"
              :type="storageForm.max_storage_mb === p.value ? 'primary' : 'default'"
              @click="storageForm.max_storage_mb = p.value"
            >
              {{ p.label }}
            </el-button>
          </div>
          <small class="muted">填 0 表示不设上限。超过配额时该租户将禁止新增单据写入。</small>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="storageOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveStorage">保存配额</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码弹窗 -->
    <el-dialog v-model="pwdOpen" title="重置租户管理员密码" width="460px">
      <el-form label-position="top">
        <el-form-item label="目标租户空间">
          <el-input :model-value="`${pwdForm.name} (${pwdForm.code})`" disabled />
        </el-form-item>

        <el-form-item :label="'新登录密码 (租户账号 ' + pwdForm.code + ') *'">
          <el-input
            v-model="pwdForm.password"
            type="password"
            show-password
            placeholder="至少 8 位密码"
          />
          <div class="mt8">
            <el-button size="small" @click="pwdForm.password = 'qq13608472109'">
              填入默认密码 (qq13608472109)
            </el-button>
          </div>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="pwdOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveTenantPwd">立即重置</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.tenants-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.head-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.head-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: #f1f5f9;
  color: #334155;
  font-size: 12px;
  font-weight: 500;
  padding: 3px 10px;
  border-radius: 999px;
  width: fit-content;
  border: 1px solid #e2e8f0;
}

.pulse-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background-color: #10b981;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.2);
}

.head-desc {
  color: #64748b;
  font-size: 13px;
  margin: 0;
  max-width: 800px;
  line-height: 1.5;
}

.btn-create {
  box-shadow: 0 2px 8px rgba(37, 99, 235, 0.25);
}

/* 平台架构说明横栏 */
.arch-card {
  display: grid;
  grid-template-columns: 1fr auto 1fr auto 1fr auto 1fr;
  align-items: center;
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 16px 20px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.03);
}

.arch-col {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.arch-icon {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
  flex-shrink: 0;
}

.arch-icon.blue { background: #eff6ff; color: #2563eb; }
.arch-icon.green { background: #ecfdf5; color: #059669; }
.arch-icon.violet { background: #f5f3ff; color: #7c3aed; }
.arch-icon.orange { background: #fffbeb; color: #d97706; }

.arch-text b {
  font-size: 13px;
  color: #1e293b;
  display: block;
  margin-bottom: 2px;
}

.arch-text p {
  font-size: 11px;
  color: #64748b;
  margin: 0;
  line-height: 1.4;
}

.arch-divider {
  width: 1px;
  height: 32px;
  background: #f1f5f9;
}

@media (max-width: 1024px) {
  .arch-card {
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  .arch-divider { display: none; }
}

/* 指标卡片 */
.metric-card {
  position: relative;
  transition: transform 0.2s, box-shadow 0.2s;
}
.metric-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.06);
}

.unit {
  font-size: 13px;
  margin-left: 6px;
  font-weight: 500;
  color: #64748b;
}

.status-tip {
  color: #2563eb;
  font-size: 11px;
}

/* 空间告警横幅 */
.alert-warning-banner {
  border-radius: 8px;
}

/* 列表主面板 */
.panel-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  padding: 16px 20px;
}

.panel-title-area h3 {
  margin: 0 0 4px 0;
  font-size: 16px;
  color: #0f172a;
}

.panel-title-area p {
  margin: 0;
  font-size: 12px;
  color: #64748b;
}

.filter-actions {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}

/* 表格内元素样式 */
.code-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.code-badge {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-weight: 700;
  color: #1e40af;
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  padding: 2px 7px;
  border-radius: 4px;
  font-size: 12px;
}

.tenant-user-tag {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: #64748b;
  margin-top: 4px;
}
.tenant-user-tag code {
  color: #0f172a;
  font-weight: 600;
}

.tenant-name-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.tenant-name {
  color: #0f172a;
  font-size: 13px;
}

.note-text {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-column {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.storage-text-row {
  display: flex;
  align-items: baseline;
  gap: 4px;
  font-size: 12px;
}

.storage-used {
  font-weight: 700;
  color: #0f172a;
  font-variant-numeric: tabular-nums;
}

.storage-sep {
  color: #94a3b8;
}

.storage-max {
  color: #475569;
  font-weight: 500;
}

.storage-pct {
  color: #64748b;
  font-size: 11px;
}

.progress-wrap {
  width: 100%;
  max-width: 200px;
}

.exceeded-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  font-weight: 600;
  color: #ef4444;
}

.db-info-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.db-path {
  font-family: monospace;
  font-size: 11px;
  color: #334155;
  background: #f8fafc;
  padding: 2px 4px;
  border-radius: 3px;
  border: 1px solid #f1f5f9;
  display: inline-block;
  word-break: break-all;
}

.db-tags {
  display: flex;
  gap: 4px;
}

.db-tag {
  font-size: 10px;
  background: #f1f5f9;
  color: #64748b;
  padding: 1px 5px;
  border-radius: 3px;
}

.status-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 500;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-dot.active {
  background-color: #10b981;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.2);
}

.status-dot.disabled {
  background-color: #ef4444;
}

.text-success { color: #059669; }
.text-danger { color: #dc2626; }

.action-buttons {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

.current-tag {
  margin-right: 6px;
}

.btn-enter {
  font-weight: 600;
}

.quota-chips {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  flex-wrap: wrap;
}

.chips-label {
  font-size: 12px;
  color: #64748b;
}

.db-preview-card {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 12px 16px;
  margin-top: 14px;
}

.preview-icon {
  font-size: 20px;
  color: #2563eb;
  margin-top: 2px;
}

.db-preview-card b {
  font-size: 12px;
  color: #1e293b;
}

.db-preview-card code {
  display: block;
  font-family: monospace;
  font-size: 12px;
  color: #2563eb;
  background: #eff6ff;
  padding: 2px 6px;
  border-radius: 4px;
  margin: 4px 0;
}

.db-preview-card p {
  margin: 0;
  font-size: 11px;
  color: #64748b;
}

.ml4 { margin-left: 4px; }
.mt8 { margin-top: 8px; }
.text-small { font-size: 11px; }
</style>
