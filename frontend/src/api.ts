import { ElMessage } from 'element-plus'

export class ApiError extends Error { constructor(message: string, public code: string) { super(message) } }
export async function api<T = any>(path: string, method = 'GET', body?: any, quiet = false): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (method !== 'GET') headers['Idempotency-Key'] = crypto.randomUUID()
  const options: RequestInit = { method, headers, credentials: 'same-origin', body: method === 'GET' ? undefined : JSON.stringify(body ?? {}) }
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
      if (response.status === 401 && !quiet) window.dispatchEvent(new Event('session-expired'))
      throw new ApiError(result.error?.message || '操作失败', result.error?.code || 'UNKNOWN')
    }
    return result.data as T
  } catch (error) {
    const e = error instanceof ApiError ? error : new ApiError('暂时无法连接本机服务，请确认客户端正在运行；未收到结果的操作可刷新核对。', 'NETWORK')
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
    const response = await fetch('/api' + path, { credentials: 'same-origin' })
    if (!response.ok) { const result = await response.json(); throw new Error(result.error?.message || '导出失败') }
    const blob = await response.blob()
    const href = URL.createObjectURL(blob)
    const a = document.createElement('a'); a.href = href
    a.download = filename || response.headers.get('Content-Disposition')?.match(/filename="([^"]+)"/)?.[1] || '业务导出.csv'
    a.click(); setTimeout(() => URL.revokeObjectURL(href), 1000)
    ElMessage.success('文件已导出')
  } catch (e: any) { ElMessage.error(e.message || '导出失败') }
}
