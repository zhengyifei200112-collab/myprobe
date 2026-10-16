export interface Unit { label: string; factor: number; hint?: string }
export const timeUnits: Unit[] = [{ label: '秒', factor: 1 }, { label: '分钟', factor: 60 }, { label: '小时', factor: 3600 }]
export const bandwidthUnits: Unit[] = [
  { label: 'B/s', factor: 1, hint: 'B 表示字节，b 表示比特；1 字节 = 8 比特。' }, { label: 'KB/s', factor: 1000, hint: '1 KB/s = 1000 字节/秒。' },
  { label: 'MB/s', factor: 1000000, hint: '1 MB/s = 1000000 字节/秒。' }, { label: 'MiB/s', factor: 1048576, hint: '1 MiB/s = 1048576 字节/秒（二进制）。' },
  { label: 'Mbps', factor: 125000, hint: 'Mbps 为十进制比特率；1 Mbps = 125000 字节/秒。' },
]
export const trafficUnits: Unit[] = [
  { label: 'B', factor: 1 }, { label: 'GB', factor: 1000000000, hint: '1 GB = 1000000000 字节。' },
  { label: 'GiB', factor: 1073741824, hint: '1 GiB = 1073741824 字节（二进制）。' }, { label: 'TB', factor: 1000000000000, hint: '1 TB = 1000000000000 字节。' },
  { label: 'TiB', factor: 1099511627776, hint: '1 TiB = 1099511627776 字节（二进制）。' },
]

// All persisted quantities are integers. Reject precision loss rather than rounding.
export function parseQuantity(text: string, factor: number): number {
  const value = text.trim()
  if (!/^\d+(?:\.\d+)?$/.test(value) || value.length > 100) throw new Error('请输入非负十进制数，不支持科学计数法。')
  if (!Number.isSafeInteger(factor) || factor < 1) throw new Error('无效单位。')
  const [whole, fraction = ''] = value.split('.')
  const denominator = 10n ** BigInt(fraction.length)
  const numerator = BigInt(whole! + fraction) * BigInt(factor)
  if (numerator % denominator !== 0n) throw new Error('精度超出最小单位，请调整数值。')
  const result = numerator / denominator
  if (result > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('数值超过可安全保存的范围。')
  return Number(result)
}

export function formatQuantity(value: number, factor: number): string {
  if (!Number.isSafeInteger(value) || value < 0) throw new Error('原值超出可安全编辑的范围。')
  if (!Number.isSafeInteger(factor) || factor < 1) throw new Error('无效单位。')
  const divisor = BigInt(factor)
  let remainder = BigInt(value) % divisor
  let result = String(BigInt(value) / divisor)
  if (!remainder) return result
  result += '.'
  for (let i = 0; remainder && i < 40; i++) {
    remainder *= 10n
    result += String(remainder / divisor)
    remainder %= divisor
  }
  if (remainder) throw new Error('此数值无法用所选单位精确表示，请保留原单位。')
  return result
}

export function currencyFactor(currency: string): number {
  const code = currency.trim().toUpperCase()
  if (!/^[A-Z]{3}$/.test(code)) throw new Error('请填写三位币种代码，例如 USD、CNY 或 JPY。')
  const digits = new Intl.NumberFormat('en', { style: 'currency', currency: code }).resolvedOptions().maximumFractionDigits
  return 10 ** (digits ?? 2)
}

// Format the exact integer/fraction separately. Dividing a large safe integer
// by 100 as a Number can lose a cent even though the stored minor units are safe.
export function formatCurrencyMinor(value: number, currency: string): string {
  const code = currency.trim().toUpperCase()
  const [whole, fraction] = formatQuantity(value, currencyFactor(code)).split('.')
  const formatter = new Intl.NumberFormat('zh-CN', { style: 'currency', currency: code, currencyDisplay: 'narrowSymbol', minimumFractionDigits: 0, useGrouping: false })
  return formatter.formatToParts(BigInt(whole!)).map(part => part.type === 'integer' && fraction ? `${part.value}.${fraction}` : part.value).join('')
}
