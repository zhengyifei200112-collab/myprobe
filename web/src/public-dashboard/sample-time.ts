// Explicit zh-CN formatting has second precision and no zone name. Key by local
// civil time so changing the browser's default timezone cannot reuse stale text.
// Keep the original formatter on misses to preserve its exact locale output.
export function createLocalSampleTimeFormatter() {
  const cache = new Map<string, string>()
  return (value: Date) => {
    const key = [value.getFullYear(), value.getMonth(), value.getDate(), value.getHours(), value.getMinutes(), value.getSeconds()].join('/')
    const prior = cache.get(key)
    if (prior !== undefined) return prior
    const text = value.toLocaleString('zh-CN', { hour12: false })
    if (cache.size >= 256) cache.delete(cache.keys().next().value!)
    cache.set(key, text)
    return text
  }
}

export const formatLocalSampleTime = createLocalSampleTimeFormatter()
