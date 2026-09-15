<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { DsButton, DsCard, DsStatusIndicator } from '../design-system'
import { backgroundVariables } from '../appearance'
import { defaultSiteSettings, type BackgroundSettings, type SiteSettings } from '../types'

type Section = 'site' | 'appearance' | 'background' | 'notifications' | 'security' | 'system' | 'maintenance'
const props = defineProps<{ settings: SiteSettings; busy: boolean }>()
const emit = defineEmits<{ save: [settings: SiteSettings]; preview: [settings: SiteSettings]; openLegacy: [section: 'security' | 'maintenance' | 'alerts'] }>()
const section = ref<Section>('site')
const draft = reactive<SiteSettings>(defaultSiteSettings())

function cloneSettings(value: SiteSettings): SiteSettings { return JSON.parse(JSON.stringify(value)) as SiteSettings }

function replaceDraft(value: SiteSettings) {
  Object.assign(draft, cloneSettings(value))
  draft.custom_links ||= []
}
watch(() => props.settings, replaceDraft, { deep: true, immediate: true })

function preview() { emit('preview', cloneSettings(draft)) }
function save() { emit('save', cloneSettings(draft)) }
function addLink() { if (draft.custom_links.length < 8) draft.custom_links.push({ label: '', url: 'https://' }) }
function removeLink(index: number) { draft.custom_links.splice(index, 1) }
function clearBackground(background: BackgroundSettings) { background.url = ''; background.blur = 0; background.opacity = 1; background.overlay = .12; preview() }

const sections: Array<{ value: Section; label: string; hint: string }> = [
  { value: 'site', label: '站点', hint: '品牌与公开信息' },
  { value: 'appearance', label: '外观', hint: '主题与强调色' },
  { value: 'background', label: '背景', hint: '页面背景图片' },
  { value: 'notifications', label: '通知', hint: '通道与规则' },
  { value: 'security', label: '登录与安全', hint: '身份与会话' },
  { value: 'system', label: '系统', hint: '运行与版本' },
  { value: 'maintenance', label: '维护', hint: '迁移与备份' },
]

const accents = [
  { value: 'blue', label: '蓝色', color: '#007aff' }, { value: 'purple', label: '紫色', color: '#af52de' },
  { value: 'green', label: '绿色', color: '#34c759' }, { value: 'orange', label: '橙色', color: '#ff9f0a' },
  { value: 'pink', label: '粉色', color: '#ff2d55' },
] as const

const backgrounds: Array<{ key: 'public_background' | 'admin_background' | 'login_background'; title: string; description: string }> = [
  { key: 'public_background', title: '公开面板', description: '访客查看节点状态时的背景。' },
  { key: 'admin_background', title: '管理后台', description: '登录后的后台工作区背景。' },
  { key: 'login_background', title: '登录页面', description: '管理员登录页面背景。' },
]
</script>

<template>
  <div class="settings-center">
    <aside class="settings-sidebar" aria-label="设置分类">
      <div class="settings-sidebar__title"><span class="eyebrow">SETTINGS</span><strong>设置中心</strong></div>
      <button v-for="item in sections" :key="item.value" :class="{ active: section === item.value }" @click="section = item.value">
        <span>{{ item.label }}</span><small>{{ item.hint }}</small>
      </button>
    </aside>

    <section class="settings-content">
      <header class="settings-content__header">
        <div><h1>{{ sections.find(item => item.value === section)?.label }}</h1><p>{{ sections.find(item => item.value === section)?.hint }}</p></div>
        <DsStatusIndicator v-if="section === 'site' || section === 'appearance' || section === 'background'" status="online" label="服务端持久化" />
      </header>

      <form v-if="section === 'site'" class="settings-stack" @submit.prevent="save">
        <DsCard as="section" padding="large" class="settings-group">
          <div class="settings-group__heading"><div><h2>站点身份</h2><p>用于浏览器、后台品牌和站点元信息。</p></div></div>
          <div class="settings-form-grid two">
            <label>站点名称<input v-model="draft.site_name" maxlength="80" required></label>
            <label>浏览器标题<input v-model="draft.browser_title" maxlength="120" required></label>
            <label class="wide">站点描述<textarea v-model="draft.site_summary" maxlength="300" rows="3"></textarea></label>
            <label>Logo URL<input v-model="draft.logo_url" type="url" placeholder="https://…"></label>
            <label>Favicon URL<input v-model="draft.favicon_url" type="url" placeholder="https://…"></label>
          </div>
        </DsCard>
        <DsCard as="section" padding="large" class="settings-group">
          <div class="settings-group__heading"><div><h2>公开面板</h2><p>定义首页首屏文案与 Agent 连接地址。</p></div></div>
          <div class="settings-form-grid two">
            <label>公开面板标题<input v-model="draft.dashboard_title" maxlength="80" required></label>
            <label>Agent 连接地址<input v-model="draft.agent_url" type="url" placeholder="留空则使用当前访问地址"></label>
            <label class="wide">公开面板描述<textarea v-model="draft.dashboard_description" maxlength="300" rows="3" required></textarea></label>
          </div>
        </DsCard>
        <DsCard as="section" padding="large" class="settings-group">
          <div class="settings-group__heading"><div><h2>页脚与链接</h2><p>为访客提供版权、联系和外部入口。</p></div><DsButton size="small" :disabled="draft.custom_links.length >= 8" @click="addLink">添加链接</DsButton></div>
          <div class="settings-form-grid two">
            <label>页脚文字<input v-model="draft.footer_text" maxlength="300"></label>
            <label>版权文字<input v-model="draft.copyright_text" maxlength="160"></label>
            <label>GitHub 链接<input v-model="draft.github_url" type="url" placeholder="https://github.com/…"></label>
            <label>博客链接<input v-model="draft.blog_url" type="url" placeholder="https://…"></label>
            <label class="wide">联系方式<input v-model="draft.contact" maxlength="200"></label>
          </div>
          <div v-if="draft.custom_links.length" class="settings-link-list">
            <div v-for="(link, index) in draft.custom_links" :key="index"><input v-model="link.label" maxlength="60" required placeholder="链接名称"><input v-model="link.url" type="url" required placeholder="https://…"><DsButton size="small" variant="ghost" @click="removeLink(index)">移除</DsButton></div>
          </div>
        </DsCard>
        <div class="settings-savebar"><span>保存后立即应用于公开面板和后台。</span><DsButton type="submit" variant="primary" :loading="busy">保存站点设置</DsButton></div>
      </form>

      <form v-else-if="section === 'appearance'" class="settings-stack" @submit.prevent="save">
        <DsCard as="section" padding="large" class="settings-group">
          <div class="settings-group__heading"><div><h2>显示模式</h2><p>系统模式会跟随访客设备的外观设置。</p></div></div>
          <div class="theme-options">
            <label v-for="item in [{value:'light',label:'浅色',hint:'明亮、低对比背景'},{value:'dark',label:'深色',hint:'适合低光环境'},{value:'system',label:'跟随系统',hint:'自动响应设备设置'}]" :key="item.value" :class="{ active: draft.theme_mode === item.value }"><input v-model="draft.theme_mode" type="radio" :value="item.value" @change="preview"><strong>{{ item.label }}</strong><small>{{ item.hint }}</small></label>
          </div>
        </DsCard>
        <DsCard as="section" padding="large" class="settings-group">
          <div class="settings-group__heading"><div><h2>强调色</h2><p>用于主操作、焦点和选中状态；状态颜色保持独立语义。</p></div></div>
          <div class="accent-options"><label v-for="item in accents" :key="item.value" :class="{ active: draft.accent_color === item.value }"><input v-model="draft.accent_color" type="radio" :value="item.value" @change="preview"><span :style="{ background: item.color }"></span>{{ item.label }}</label></div>
        </DsCard>
        <div class="settings-savebar"><span>当前选择会即时预览，保存后同步给所有访客。</span><DsButton type="submit" variant="primary" :loading="busy">保存外观</DsButton></div>
      </form>

      <form v-else-if="section === 'background'" class="settings-stack" @submit.prevent="save">
        <DsCard v-for="item in backgrounds" :key="item.key" as="section" padding="large" class="settings-group background-editor">
          <div class="settings-group__heading"><div><h2>{{ item.title }}</h2><p>{{ item.description }}</p></div><DsButton size="small" variant="ghost" @click="clearBackground(draft[item.key])">清除图片</DsButton></div>
          <div class="background-editor__preview" :style="backgroundVariables(draft[item.key])"><span>{{ draft[item.key].url ? '背景预览' : '默认简洁背景' }}</span></div>
          <div class="settings-form-grid two">
            <label class="wide">图片 URL<input v-model="draft[item.key].url" type="url" placeholder="https://…" @change="preview"></label>
            <label>显示方式<select v-model="draft[item.key].fit" @change="preview"><option value="cover">Cover</option><option value="contain">Contain</option><option value="original">Original</option></select></label>
            <label>背景位置<select v-model="draft[item.key].position" @change="preview"><option value="center">Center</option><option value="top">Top</option><option value="bottom">Bottom</option></select></label>
            <label>模糊 {{ draft[item.key].blur }}px<input v-model.number="draft[item.key].blur" type="range" min="0" max="24" @input="preview"></label>
            <label>图片透明度 {{ Math.round(draft[item.key].opacity * 100) }}%<input v-model.number="draft[item.key].opacity" type="range" min="0" max="1" step="0.05" @input="preview"></label>
            <label>遮罩 {{ Math.round(draft[item.key].overlay * 100) }}%<input v-model.number="draft[item.key].overlay" type="range" min="0" max="0.8" step="0.05" @input="preview"></label>
          </div>
        </DsCard>
        <div class="settings-savebar"><span>远程图片失败时自动回退到默认背景。</span><DsButton type="submit" variant="primary" :loading="busy">保存背景</DsButton></div>
      </form>

      <DsCard v-else padding="large" class="settings-placeholder">
        <span class="eyebrow">PROGRESSIVE DELIVERY</span>
        <h2>{{ section === 'notifications' ? '通知系统已启用' : section === 'security' ? '登录与安全' : section === 'maintenance' ? '现有维护工具已可使用' : '系统信息' }}</h2>
        <p v-if="section === 'notifications'">管理 Provider、告警规则、消息模板和发送历史。</p>
        <p v-else-if="section === 'security'">管理密码登录、GitHub OAuth 白名单、会话安全与审计记录。</p>
        <p v-else-if="section === 'maintenance'">配置迁移和加密数据库备份未发生变化，可继续使用现有工具。</p>
        <p v-else>MyProbe 的运行配置沿用当前安全默认值；本阶段不修改服务端部署参数。</p>
        <DsButton v-if="section === 'notifications'" @click="emit('openLegacy', 'alerts')">打开当前告警页面</DsButton>
        <DsButton v-else-if="section === 'security'" @click="emit('openLegacy', 'security')">打开登录与安全</DsButton>
        <DsButton v-else-if="section === 'maintenance'" @click="emit('openLegacy', 'maintenance')">打开迁移与备份</DsButton>
      </DsCard>
    </section>
  </div>
</template>
