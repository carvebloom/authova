import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const demo=JSON.parse(readFileSync('.dev/rp.json','utf8'))
const password='A verified local phone password phrase'
const phone='+8613800138000'
const changedPhone='+8613800138001'
async function receiveCode(request:any,destination:string,seen:Set<string>){let code='';await expect.poll(async()=>{const response=await request.get('http://127.0.0.1:18089/messages');const inbox=await response.json();const item=inbox.find((m:any)=>Boolean(m.app_id) && m.destination===destination && !seen.has(m.message_id));code=item?.body.match(/code: ([0-9]{6})/)?.[1]??'';return Boolean(code)},{timeout:20000}).toBe(true);return code}
async function seenMessages(request:any){return new Set<string>((await(await request.get('http://127.0.0.1:18089/messages')).json()).map((m:any)=>m.message_id))}
test('phone registration, SMS OTP login, and verified phone replacement preserve one account',async({page,request})=>{
 const app=demo.apps[0]
 // Each run uses a distinct E.164 suffix to keep the persistent Demo repeatable.
 const suffix=String(Date.now()).slice(-8)
 const registrationPhone=phone.slice(0,-8)+suffix
 const replacementPhone=changedPhone.slice(0,-8)+String(Number(suffix)+1).padStart(8,'0')
 await page.goto(`http://127.0.0.1:18088/apps/${app.key}/login`)
 await page.getByText('注册、验证码登录或找回密码',{exact:true}).click()
 await page.getByLabel('验证渠道').selectOption('phone')
 await page.getByLabel('地址',{exact:true}).fill(registrationPhone)
 let seen=await seenMessages(request)
 await page.getByRole('button',{name:'发送验证码',exact:true}).click()
 await page.getByLabel('验证码',{exact:true}).fill(await receiveCode(request,registrationPhone,seen))
 await page.getByRole('button',{name:'验证并继续',exact:true}).click()
 await page.getByLabel('昵称').fill('Phone Demo User')
 await page.getByLabel('设置密码').fill(password)
 await page.getByRole('button',{name:'创建账号',exact:true}).click()
 await page.getByRole('button',{name:'允许并继续',exact:true}).click()
 await expect(page).toHaveURL(new RegExp(`/apps/${app.key}/home`))
 const subject=await page.locator('dd').first().innerText()
 await page.goto(`http://127.0.0.1:18080/a/${app.key}/ui/account`)
 await page.getByText('更换邮箱或手机号',{exact:true}).click()
 await page.getByLabel('类型').selectOption('phone')
 await page.getByLabel('新地址',{exact:true}).fill(replacementPhone)
 seen=await seenMessages(request)
 await page.getByRole('button',{name:'验证新地址',exact:true}).click()
 await page.getByLabel('验证码',{exact:true}).fill(await receiveCode(request,replacementPhone,seen))
 await page.getByRole('button',{name:'验证并继续',exact:true}).click()
 await page.getByRole('button',{name:'确认更换并撤销旧会话',exact:true}).click()
 await expect(page.getByText('账号资料已更新。请重新登录。',{exact:true})).toBeVisible()
 await page.goto(`http://127.0.0.1:18088/apps/${app.key}/login`)
 await page.getByLabel('账号类型').selectOption('phone')
 await page.getByLabel('邮箱 / 手机号').fill(replacementPhone)
 await page.getByLabel('密码',{exact:true}).fill(password)
 await page.getByRole('button',{name:'登录',exact:true}).click()
 await page.getByRole('button',{name:'允许并继续',exact:true}).click()
 await expect(page.locator('dd').first()).toHaveText(subject)
 await page.goto(`http://127.0.0.1:18080/a/${app.key}/ui/logout`)
 await page.getByRole('button',{name:'确认退出',exact:true}).click()
 await page.goto(`http://127.0.0.1:18088/apps/${app.key}/login`)
 await page.getByText('注册、验证码登录或找回密码',{exact:true}).click()
 await page.getByLabel('操作').selectOption('login')
 await page.getByLabel('验证渠道').selectOption('phone')
 await page.getByLabel('地址',{exact:true}).fill(replacementPhone)
 seen=await seenMessages(request)
 await page.getByRole('button',{name:'发送验证码',exact:true}).click()
 await page.getByLabel('验证码',{exact:true}).fill(await receiveCode(request,replacementPhone,seen))
 await page.getByRole('button',{name:'验证并继续',exact:true}).click()
 await page.getByRole('button',{name:'允许并继续',exact:true}).click()
 await expect(page.locator('dd').first()).toHaveText(subject)
 await page.goto('http://127.0.0.1:18089')
 await expect(page.getByRole('heading',{name:'本地短信收件箱',exact:true})).toBeVisible()
 // Screenshots intentionally omit OTPs and phone identifiers; the mailbox is a private diagnostic surface.
})
