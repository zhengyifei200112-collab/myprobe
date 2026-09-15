<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { loadAuthSettings, updateAuthSettings } from '../admin-api'
import { DsBadge, DsButton, DsCard, DsStatusIndicator } from '../design-system'

const emit=defineEmits<{notice:[value:string];error:[value:unknown]}>()
const busy=ref(false);const loaded=ref(false)
const form=reactive({enabled:false,client_id:'',client_secret:'',client_secret_set:false,callback_url:`${location.origin}/api/v1/auth/github/callback`,allowlist:'' as string,verified_at:''})
async function load(){busy.value=true;try{const result=await loadAuthSettings();Object.assign(form,{enabled:result.github.enabled,client_id:result.github.client_id,client_secret:'',client_secret_set:result.github.client_secret_set,callback_url:result.github.callback_url||`${location.origin}/api/v1/auth/github/callback`,allowlist:result.github.username_allowlist.join('\n'),verified_at:result.github.verified_at||''});loaded.value=true}catch(e){emit('error',e)}finally{busy.value=false}}
async function save(){busy.value=true;try{const result=await updateAuthSettings({enabled:form.enabled,client_id:form.client_id.trim(),client_secret:form.client_secret,callback_url:form.callback_url.trim(),username_allowlist:form.allowlist.split(/[,\n]/).map(x=>x.trim()).filter(Boolean)});Object.assign(form,{enabled:result.github.enabled,client_id:result.github.client_id,client_secret:'',client_secret_set:result.github.client_secret_set,callback_url:result.github.callback_url,allowlist:result.github.username_allowlist.join('\n'),verified_at:result.github.verified_at||''});emit('notice','登录与安全设置已保存。')}catch(e){emit('error',e)}finally{busy.value=false}}
function verifyGitHub(){location.assign('/api/v1/auth/github/start')}
onMounted(load)
</script>

<template>
  <DsCard padding="large" class="auth-provider-card">
    <header><div><span class="eyebrow">AUTH PROVIDERS</span><h2>登录方式</h2><p>Password 始终作为安全回退；GitHub OAuth 仅允许白名单账号。</p></div><DsStatusIndicator status="online" label="密码登录可用" /></header>
    <div v-if="loaded" class="provider-stack">
      <section class="provider-row"><div class="provider-logo password-logo">•••</div><div><strong>Password</strong><small>bcrypt 哈希 · 登录限速 · CAPTCHA</small></div><DsBadge tone="success">已启用</DsBadge></section>
      <section class="github-config">
        <div class="provider-row"><div class="provider-logo github-logo">GH</div><div><strong>GitHub OAuth</strong><small>{{ form.verified_at ? `已验证 · ${new Date(form.verified_at).toLocaleString('zh-CN',{hour12:false})}` : '尚未完成实际登录验证' }}</small></div><label class="provider-toggle"><input v-model="form.enabled" type="checkbox"><span>{{ form.enabled?'启用':'停用' }}</span></label></div>
        <form class="auth-form" @submit.prevent="save">
          <div class="form-grid two"><label>Client ID<input v-model="form.client_id" :required="form.enabled" autocomplete="off" placeholder="Ov23li…"></label><label>Client Secret<input v-model="form.client_secret" type="password" :required="form.enabled&&!form.client_secret_set" autocomplete="new-password" :placeholder="form.client_secret_set?'已安全保存；留空保持不变':'输入 GitHub Client Secret'"><small>{{ form.client_secret_set?'Secret 已使用 AES-GCM 加密，读取接口不会返回明文。':'尚未保存 Secret。' }}</small></label></div>
          <label>Callback URL<input v-model="form.callback_url" type="url" :required="form.enabled" autocomplete="off"><small>请将此完整地址填写到 GitHub OAuth App 的 Authorization callback URL。</small></label>
          <label>GitHub Username 白名单<textarea v-model="form.allowlist" rows="5" :required="form.enabled" placeholder="usernameA&#10;usernameB"></textarea><small>每行一个用户名；比较时忽略大小写，保存后统一规范化。</small></label>
          <div class="lockout-note"><strong>管理员防锁死</strong><span>密码登录不会被关闭。即使 OAuth 配置错误，仍可使用管理员密码登录。</span></div>
          <footer><DsButton type="submit" variant="primary" :loading="busy">保存登录设置</DsButton><DsButton v-if="form.enabled&&form.client_secret_set" type="button" @click="verifyGitHub">验证 GitHub 登录</DsButton></footer>
        </form>
      </section>
    </div>
  </DsCard>
</template>

<style scoped>
.auth-provider-card{grid-column:1/-1}.auth-provider-card>header{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.auth-provider-card h2{margin:5px 0 6px}.auth-provider-card p{margin:0;color:var(--muted);font-size:11px}.provider-stack{display:grid;gap:12px;margin-top:20px}.provider-row{display:flex;align-items:center;gap:12px;padding:14px;border:1px solid var(--border);border-radius:14px;background:var(--surface-subtle)}.provider-row>div:nth-child(2){display:grid;flex:1;gap:3px}.provider-row small{color:var(--muted);font-size:10px}.provider-logo{display:grid;width:40px;height:40px;place-items:center;border-radius:12px;font-size:10px;font-weight:850}.password-logo{background:color-mix(in srgb,var(--green) 12%,var(--surface));color:var(--green)}.github-logo{background:var(--text);color:var(--background)}.github-config{border:1px solid var(--border);border-radius:16px;overflow:hidden}.github-config>.provider-row{border:0;border-radius:0}.provider-toggle{display:flex;align-items:center;gap:7px;font-size:10px}.auth-form{display:grid;gap:14px;padding:18px}.auth-form label{display:grid;gap:6px;color:var(--muted);font-size:10px;font-weight:650}.auth-form label small{font-weight:400}.auth-form footer{display:flex;gap:8px}.lockout-note{display:grid;gap:4px;padding:12px 14px;border-radius:12px;background:color-mix(in srgb,var(--blue) 7%,var(--surface));font-size:10px}.lockout-note span{color:var(--muted)}@media(max-width:640px){.auth-provider-card>header{flex-direction:column}.provider-row{align-items:flex-start}.provider-toggle{margin-left:auto}.auth-form footer{align-items:stretch;flex-direction:column}}
</style>
