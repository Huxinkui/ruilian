import { ElMessage } from 'element-plus'

export class ApiError extends Error { constructor(message: string, public code: string) { super(message) } }

export function getTenantCode(): string {
  return localStorage.getItem('ruilian_tenant') || 'default'
}
export function setTenantCode(code: string) {
  localStorage.setItem('ruilian_tenant', code || 'default')
}
export function getToken(): string {
  return localStorage.getItem('ruilian_token') || ''
}
export function setToken(token: string) {
  if (token) localStorage.setItem('ruilian_token', token)
  else localStorage.removeItem('ruilian_token')
}

export function safeUUID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    try { return crypto.randomUUID() } catch {}
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

export async function api<T = any>(path: string, method = 'GET', body?: any, quiet = false): Promise<T> {
  const token = getToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-Tenant-Code': getTenantCode()
  }
  if (token) {
    headers['Authorization'] = 'Bearer ' + token
    headers['X-Session-Token'] = token
  }
  if (method !== 'GET') headers['Idempotency-Key'] = safeUUID()
  const options: RequestInit = { method, headers, credentials: 'include', body: method === 'GET' ? undefined : JSON.stringify(body ?? {}) }
  try {
    let response: Response
    try { response = await fetch('/api' + path, options) }
    catch (error) {
      if (method !== 'GET' && (path.startsWith('/documents') || path.startsWith('/finance'))) {
        await new Promise(resolve => setTimeout(resolve, 600))
        response = await fetch('/api' + path, options)
      } else throw error
    }
    const result = await response.json()
    if (!result.ok) {
      if (response.status === 401) {
        setToken('')
        if (!quiet) window.dispatchEvent(new Event('session-expired'))
      }
      throw new ApiError(result.error?.message || '操作失败', result.error?.code || 'UNKNOWN')
    }
    return result.data as T
  } catch (error) {
    const e = error instanceof ApiError ? error : new ApiError('暂时无法连接服务端，请确认服务已启动（默认端口 3001）；未收到结果的操作可刷新核对。', 'NETWORK')
    if (!quiet) ElMessage.error({ message: e.message, duration: 5500, grouping: true })
    throw e
  }
}
export function query(values: Record<string, any>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(values)) if (value !== '' && value !== null && value !== undefined) params.set(key, String(value))
  return params.toString()
}
export async function download(path: string, filename?: string) {
  try {
    const token = getToken()
    const headers: Record<string, string> = { 'X-Tenant-Code': getTenantCode() }
    if (token) {
      headers['Authorization'] = 'Bearer ' + token
      headers['X-Session-Token'] = token
    }
    const response = await fetch('/api' + path, { credentials: 'include', headers })
    if (!response.ok) { const result = await response.json(); throw new Error(result.error?.message || '导出失败') }
    const blob = await response.blob()
    const href = URL.createObjectURL(blob)
    const a = document.createElement('a'); a.href = href
    a.download = filename || response.headers.get('Content-Disposition')?.match(/filename="([^"]+)"/)?.[1] || '业务导出.csv'
    a.click(); setTimeout(() => URL.revokeObjectURL(href), 1000)
    ElMessage.success('文件已导出')
  } catch (e: any) { ElMessage.error(e.message || '导出失败') }
}
