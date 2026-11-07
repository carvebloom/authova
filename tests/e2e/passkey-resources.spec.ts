import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import { readFileSync, mkdirSync } from 'node:fs'
import { createHmac } from 'node:crypto'

const enabled = process.env.AUTHOVA_PASSKEY_DEMO === '1'
const config = enabled ? JSON.parse(readFileSync('.dev/passkey-demo/rp.json','utf8')) : null
const bootstrap = enabled ? JSON.parse(readFileSync('.dev/passkey-demo/bootstrap.json','utf8')) : null
const core='http://localhost:18180',rp='http://localhost:18188',consoleURL='http://localhost:18190'
const screenshots='output/playwright/passkeys'
function totp(secret:string){const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of secret)bits+=alphabet.indexOf(c).toString(2).padStart(5,'0');const key=Buffer.from(bits.match(/.{8}/g)!.map(b=>parseInt(b,2)));const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const hash=createHmac('sha1',key).update(counter).digest();return String((hash.readUInt32BE(hash[hash.length-1]!&15)&0x7fffffff)%1000000).padStart(6,'0')}
async function register(page:Page,request:APIRequestContext,app:any,email:string){
  console.log('passkey registration: open login')
  await page.goto(`${rp}/apps/${app.key}/login`)
  await page.getByText('注册、验证码登录或找回密码',{exact:true}).click()
  console.log('passkey registration: choose operation')
  await page.getByRole('combobox',{name:'操作',exact:true}).selectOption('register')
  console.log('passkey registration: enter test address')
  await page.getByLabel('地址',{exact:true}).fill(email)
  console.log('passkey registration: read old inbox')
  const old=await(await request.get('http://127.0.0.1:58025/api/v1/messages')).json();const seen=new Set(old.messages.map((m:any)=>m.ID))
  console.log('passkey registration: request code')
  await page.getByRole('button',{name:'发送验证码',exact:true}).click()
  await expect(page.getByLabel('验证码',{exact:true})).toBeVisible()
  let id='';await expect.poll(async()=>{const inbox=await(await request.get('http://127.0.0.1:58025/api/v1/messages')).json();id=inbox.messages.find((m:any)=>!seen.has(m.ID)&&m.To.some((to:any)=>to.Address===email))?.ID??'';return !!id},{timeout:20000}).toBe(true)
  const delivered=await(await request.get(`http://127.0.0.1:58025/api/v1/message/${id}`)).json();const code=delivered.Text.match(/code: ([0-9]{6})/)?.[1];if(!code)throw new Error('Verification delivery missing')
  await page.getByLabel('验证码',{exact:true}).fill(code);await page.getByRole('button',{name:'验证并继续',exact:true}).click()
  console.log('passkey registration: create authenticator credential')
  const form=page.locator('form[data-passkey-register]');await form.getByLabel('昵称').fill('Passkey browser user');await form.getByLabel('Passkey 名称').fill('Browser device')
  await page.getByRole('button',{name:'创建 Passkey 并注册',exact:true}).click()
  await expect(page.getByRole('heading',{name:'确认授权',exact:true})).toBeVisible()
  await page.getByRole('button',{name:'允许并继续',exact:true}).click()
  await expect(page).toHaveURL(new RegExp(`/apps/${app.key}/home$`))
  return (await page.locator('dd').first().innerText()).trim()
}
async function logout(page:Page,app:any){await page.goto(`${core}/a/${app.key}/ui/logout`);await page.getByRole('button',{name:'确认退出',exact:true}).click();await expect(page.getByText('已退出当前 App。',{exact:true})).toBeVisible()}

test.describe('isolated localhost Passkey and resource acceptance',()=>{
 test.skip(!enabled,'Start scripts/passkey-demo.py and opt in with AUTHOVA_PASSKEY_DEMO=1')
 test('Chrome virtual authenticator registers, logs in, isolates Apps and removes credentials',async({page,request})=>{
  test.setTimeout(180000);page.setDefaultTimeout(15000);mkdirSync(screenshots,{recursive:true})
  const cdp=await page.context().newCDPSession(page);await cdp.send('WebAuthn.enable');const {authenticatorId}=await cdp.send('WebAuthn.addVirtualAuthenticator',{options:{protocol:'ctap2',transport:'internal',hasResidentKey:true,hasUserVerification:true,isUserVerified:true,automaticPresenceSimulation:true}})
  const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
  try {
   const [app1,app2]=config.apps;const email=`passkey-${Date.now()}@example.test`
   const subject1=await register(page,request,app1,email)
   await page.goto(`${core}/a/${app1.key}/ui/account`);await expect(page.getByRole('list',{name:'已注册的 Passkey'})).toContainText('Browser device');await page.screenshot({path:`${screenshots}/account.png`,fullPage:true})
   await logout(page,app1)
   await page.goto(`${rp}/apps/${app1.key}/login`);await page.getByRole('button',{name:'使用 Passkey 登录',exact:true}).click();await expect(page.getByRole('heading',{name:'确认授权',exact:true})).toBeVisible();await page.getByRole('button',{name:'允许并继续',exact:true}).click();await expect(page.locator('dd').first()).toHaveText(subject1)
   await logout(page,app1)
   // Both Apps share an RP ID. A valid credential from App 1 must still be rejected in App 2.
   await page.goto(`${rp}/apps/${app2.key}/login`);await page.getByRole('button',{name:'使用 Passkey 登录',exact:true}).click();await expect(page.locator('[data-passkey-status]')).toContainText('authentication_required');await expect(page).toHaveURL(/\/ui\/flows\//)
   const subject2=await register(page,request,app2,email);expect(subject2).not.toBe(subject1)
   await page.goto(`${core}/a/${app2.key}/ui/account`);const list=page.getByRole('list',{name:'已注册的 Passkey'});await expect(list.locator('li')).toHaveCount(1)
   await cdp.send('WebAuthn.removeVirtualAuthenticator',{authenticatorId});await cdp.send('WebAuthn.addVirtualAuthenticator',{options:{protocol:'ctap2',transport:'internal',hasResidentKey:true,hasUserVerification:true,isUserVerified:true,automaticPresenceSimulation:true}})
   // The finish commits, then the connection is lost: freeze until authoritative read-back.
   let finishes=0
   await page.route(`**/a/${app2.key}/ui/account/passkeys/register/finish`,async route=>{finishes++;await route.fetch();await route.abort('failed')})
   await page.getByLabel('Passkey 名称',{exact:true}).fill('Second device');await page.getByRole('button',{name:'添加 Passkey',exact:true}).click();await expect(page.locator('[data-passkey-status]')).toContainText('结果未知');await expect(page.getByRole('button',{name:'添加 Passkey',exact:true})).toBeDisabled();expect(finishes).toBe(1);await page.unroute(`**/a/${app2.key}/ui/account/passkeys/register/finish`);await page.getByRole('button',{name:'重新读取 Passkey 列表',exact:true}).click();await expect(list.locator('li')).toHaveCount(2);expect(finishes).toBe(1)
   page.once('dialog',dialog=>dialog.accept());await page.getByRole('button',{name:'删除 Passkey Second device',exact:true}).click();await expect(list.locator('li')).toHaveCount(1);await expect(page.locator('[data-passkey-status]')).toContainText('已删除')
   await page.setViewportSize({width:390,height:844});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:`${screenshots}/account-mobile.png`,fullPage:true});expect(errors).toEqual([])
  } finally { /* The test-owned browser context destroys its virtual authenticator on close. */ }
 })
 test('Console edits resource budgets and restores authoritative state',async({page})=>{
  test.setTimeout(120000);const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(consoleURL)
  try {await page.getByLabel(/^邮箱/).fill(bootstrap.email);await page.getByLabel(/^密码/).fill(bootstrap.password);await page.getByLabel(/^动态口令/).fill(totp(bootstrap.totp_secret))}catch{throw new Error('Private operator credential input failed')}
  await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page.getByRole('heading',{name:'应用',exact:true})).toBeVisible();const app=config.apps[0];await page.goto(`${consoleURL}/#/apps/${app.id}/settings`)
  const resources=page.getByRole('region',{name:'资源隔离'});await expect(resources.getByText('调整资源预算')).toBeVisible();await resources.getByText('调整资源预算').click();const input=resources.getByLabel('每秒请求上限');const original=await input.inputValue();await input.fill(String(Number(original)-1));await resources.getByLabel('资源调整原因').fill('Browser resource isolation acceptance');await resources.getByRole('button',{name:'保存资源预算'}).click();await expect(resources.getByRole('status')).toContainText('已更新')
  await input.fill(original);await resources.getByLabel('资源调整原因').fill('Restore baseline after browser acceptance');await resources.getByRole('button',{name:'保存资源预算'}).click();await expect(resources.getByRole('button',{name:'保存资源预算'})).toBeDisabled();await page.screenshot({path:`${screenshots}/console-resources.png`,fullPage:true})
  await page.setViewportSize({width:390,height:844});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:`${screenshots}/console-resources-mobile.png`,fullPage:true});expect(errors).toEqual([])
 })
 test('existing 18090 demo retains operator access and exposes upgraded configuration',async({page})=>{
  test.skip(process.env.AUTHOVA_VERIFY_ORIGINAL_DEMO!=='1','Explicit opt-in for retained original demo')
  const original=JSON.parse(readFileSync('.dev/bootstrap.json','utf8'));const apps=JSON.parse(readFileSync('.dev/rp.json','utf8')).apps
  await page.goto('http://127.0.0.1:18090')
  try {await page.getByLabel(/^邮箱/).fill(original.email);await page.getByLabel(/^密码/).fill(original.password);await page.getByLabel(/^动态口令/).fill(totp(original.totp_secret))}catch{throw new Error('Original credential input failed; details suppressed')}
  await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page.getByRole('heading',{name:'应用',exact:true})).toBeVisible()
  const appLink=page.getByRole('link',{name:apps[0].name,exact:true});const href=await appLink.getAttribute('href');if(!href)throw new Error('Original App link missing')
  await page.goto('http://127.0.0.1:18090/'+href+'/settings')
  await expect(page.getByRole('region',{name:'资源隔离'}).getByText('调整资源预算')).toBeVisible();await expect(page.getByRole('checkbox',{name:/Passkey/})).not.toBeChecked()
 })

})
