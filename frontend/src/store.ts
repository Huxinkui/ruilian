import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from './api'
import defaultBrand from './default-brand.json'

export const useApp = defineStore('app', () => {
  const user = ref<any>(null), license = ref<any>({ writable: false }), company = ref('我的企业'), needsSetup = ref(false), ready = ref(false)
  const branding = ref({ ...defaultBrand })
  const kinds = ref<any[]>([]), permissionGroups = ref<Record<string, string[]>>({}), tick = ref(0), warningCount = ref(0)
  const editor = ref<{ visible: boolean; id: number; kind: string; preset: any }>({ visible: false, id: 0, kind: '', preset: null })
  const can = (permission: string) => !!user.value && (user.value.permissions.includes('*') || user.value.permissions.includes(permission))
  const amountVisible = computed(() => can('amount.view')), costVisible = computed(() => can('cost.view'))
  const writable = (module: string) => !!license.value.writable && (!license.value.license || license.value.license.features.includes('*') || license.value.license.features.includes(module))
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
      updateBranding(status.branding)
      if (!needsSetup.value) { try { await refreshMe() } catch { user.value = null } }
    } finally { ready.value = true }
  }
  async function refreshMe() {
    const result = await api('/me', 'GET', undefined, true)
    user.value = result.user; license.value = result.license; company.value = result.company_name
    updateBranding(result.branding)
    kinds.value = result.kinds; permissionGroups.value = result.permission_groups
  }
  function openDoc(id = 0, kind = '', preset: any = null) { editor.value = { visible: true, id, kind, preset } }
  function changed() { tick.value++ }
  return { user, license, company, branding, needsSetup, ready, kinds, permissionGroups, tick, warningCount, editor, can, writable, amountVisible, costVisible, bootstrap, refreshMe, openDoc, changed }
})
