// OAuth returns to /admin. Only a local node-detail path may be restored from
// tab-scoped storage; never treat a caller-supplied URL as a general redirect.
export function adminDetailDestination(value: string | null): string | null {
  if (!value || !value.startsWith('/admin/nodes/') || value.includes('\\')) return null
  try {
    const url = new URL(value, 'https://local.invalid')
    if (url.origin !== 'https://local.invalid' || !/^\/admin\/nodes\/[^/]{1,128}\/?$/.test(url.pathname)) return null
    return url.pathname + url.search
  } catch { return null }
}
