export interface Unit { label: string; factor: number }
export const timeUnits: Unit[] = [{ label: '秒', factor: 1 }, { label: '分钟', factor: 60 }, { label: '小时', factor: 3600 }]
export const bandwidthUnits: Unit[] = [
  { label: 'B/s', factor: 1 }, { label: 'KB/s', factor: 1000 },
  { label: 'MB/s', factor: 1000000 }, { label: 'MiB/s', factor: 1048576 },
  { label: 'Mbps', factor: 125000 },
]
export const trafficUnits: Unit[] = [
  { label: 'B', factor: 1 }, { label: 'GB', factor: 1000000000 },
  { label: 'GiB', factor: 1073741824 }, { label: 'TB', factor: 1000000000000 },
  { label: 'TiB', factor: 1099511627776 },
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
