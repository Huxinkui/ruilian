import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from './api'
import defaultBrand from './default-brand.json'

export const useApp = defineStore('app', () => {
  const user = ref<any>(null), license = ref<any>({ writable: true, status: 'active' }), company = ref('我的企业'), needsSetup = ref(false), ready = ref(false)
  const tenant = ref<any>({ code: 'default', name: '默认租户', max_storage_mb: 500, used_storage_mb: 0, storage_percent: 0 })
  const branding = ref({ ...defaultBrand })
  const kinds = ref<any[]>([]), permissionGroups = ref<Record<string, string[]>>({}), tick = ref(0), warningCount = ref(0)
  const editor = ref<{ visible: boolean; id: number; kind: string; preset: any }>({ visible: false, id: 0, kind: '', preset: null })
  const tenantList = ref<any[]>([])
  const can = (permission: string) => !!user.value && (user.value.permissions.includes('*') || user.value.permissions.includes(permission))
  const isMasterAdmin = computed(() => !!user.value && (user.value.username === 'huxinkui' || !!user.value.is_master_admin) && (tenant.value?.code === 'default' || !tenant.value?.code))
  const amountVisible = computed(() => can('amount.view')), costVisible = computed(() => can('cost.view'))
  const writable = (_module = '') => true
  function updateBranding(value: typeof defaultBrand) {
    branding.value = value || { ...defaultBrand }
    document.title = branding.value.app_name
    const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (icon) { icon.href = branding.value.brand_logo || '/brand-default.png'; icon.removeAttribute('type') }
    void window.ruilianDesktop?.refreshBranding().catch(() => {})
  }
  async function bootstrap() {
    try {
      const status = await api('/status', 'GET', undefined, true)
      needsSetup.value = status.needs_setup; company.value = status.company_name; license.value = status.license
      if (status.tenant) tenant.value = status.tenant
      updateBranding(status.branding)
      if (!needsSetup.value) {
        try {
          await refreshMe()
          await loadTenantList()
        } catch { user.value = null }
      }
    } finally { ready.value = true }
  }
  async function refreshMe() {
    const result = await api('/me', 'GET', undefined, true)
    user.value = result.user; license.value = result.license; company.value = result.company_name
    if (result.tenant) tenant.value = result.tenant
    updateBranding(result.branding)
    kinds.value = result.kinds; permissionGroups.value = result.permission_groups
  }
  async function loadTenantList() {
    if (user.value && (user.value.is_master_admin || user.value.username === 'huxinkui')) {
      try {
        const res = await api('/system/tenants', 'GET', undefined, true)
        tenantList.value = res.items || res.tenants || []
      } catch {
        tenantList.value = []
      }
    } else {
      tenantList.value = []
    }
  }
  async function switchTenant(code: string) {
    const target = code || 'default'
    localStorage.setItem('ruilian_tenant', target)
    document.cookie = `ruilian_tenant=${target}; path=/; max-age=43200; SameSite=Lax`
    await refreshMe()
    await loadTenantList()
    changed()
  }
  function openDoc(id = 0, kind = '', preset: any = null) { editor.value = { visible: true, id, kind, preset } }
  function changed() { tick.value++ }
  return { user, license, company, tenant, tenantList, branding, needsSetup, ready, kinds, permissionGroups, tick, warningCount, editor, can, isMasterAdmin, writable, amountVisible, costVisible, bootstrap, refreshMe, loadTenantList, switchTenant, openDoc, changed }
})
