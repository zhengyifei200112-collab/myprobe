import { defaultSiteSettings, type AccentColor, type BackgroundSettings, type SiteSettings, type ThemeMode } from './types'

const accentValues: Record<AccentColor, { base: string; hover: string; pressed: string; soft: string }> = {
  blue: { base: '#007aff', hover: '#0071e3', pressed: '#0062cc', soft: 'rgba(0, 122, 255, .11)' },
  purple: { base: '#af52de', hover: '#9f45cf', pressed: '#8d38bb', soft: 'rgba(175, 82, 222, .12)' },
  green: { base: '#248a3d', hover: '#1f7a35', pressed: '#19692d', soft: 'rgba(52, 199, 89, .12)' },
  orange: { base: '#e87500', hover: '#d66c00', pressed: '#c36000', soft: 'rgba(255, 159, 10, .13)' },
  pink: { base: '#e31b63', hover: '#d41459', pressed: '#bf0c4e', soft: 'rgba(255, 45, 85, .12)' },
}

export function applyAppearance(settings: SiteSettings, localTheme?: ThemeMode) {
  const root = document.documentElement
  root.dataset.theme = localTheme || settings.theme_mode || 'system'
  root.dataset.accent = settings.accent_color || 'blue'
  const accent = accentValues[settings.accent_color || 'blue']
  root.style.setProperty('--accent', accent.base)
  root.style.setProperty('--accent-hover', accent.hover)
  root.style.setProperty('--accent-pressed', accent.pressed)
  root.style.setProperty('--accent-soft', accent.soft)
  updateThemeColor()
  document.title = settings.browser_title || `${settings.site_name || 'MyProbe'} · 服务器探针`
  updateFavicon(settings.favicon_url)
}

export function cacheAppearance(settings: SiteSettings) {
  localStorage.setItem('myprobe-site-appearance', JSON.stringify({ theme_mode: settings.theme_mode, accent_color: settings.accent_color }))
}

export function applyCachedAppearance() {
  try {
    const cached = JSON.parse(localStorage.getItem('myprobe-site-appearance') || '{}') as Pick<SiteSettings, 'theme_mode' | 'accent_color'>
    const root = document.documentElement
    root.dataset.theme = cached.theme_mode || 'system'
    const accent = accentValues[cached.accent_color || 'blue']
    root.dataset.accent = cached.accent_color || 'blue'
    root.style.setProperty('--accent', accent.base)
    root.style.setProperty('--accent-hover', accent.hover)
    root.style.setProperty('--accent-pressed', accent.pressed)
    root.style.setProperty('--accent-soft', accent.soft)
    updateThemeColor()
  } catch { document.documentElement.dataset.theme = 'system' }
}

export function updateThemeColor() {
  let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (!meta) {
    meta = document.createElement('meta')
    meta.name = 'theme-color'
    document.head.appendChild(meta)
  }
  meta.content = getComputedStyle(document.documentElement).getPropertyValue('--background').trim() || '#f5f5f7'
}

export function backgroundVariables(background?: BackgroundSettings) {
  background ||= defaultSiteSettings().public_background
  const size = background.fit === 'original' ? 'auto' : background.fit
  return {
    '--site-background-image': background.url ? `url("${background.url.replaceAll('"', '%22')}")` : 'none',
    '--site-background-size': size,
    '--site-background-position': background.position,
    '--site-background-blur': `${background.blur}px`,
    '--site-background-opacity': String(background.opacity),
    '--site-background-overlay': String(background.overlay),
  }
}

function updateFavicon(url: string) {
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"][data-myprobe]')
  if (!url) { link?.remove(); return }
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    link.dataset.myprobe = 'true'
    document.head.appendChild(link)
  }
  link.href = url
}
