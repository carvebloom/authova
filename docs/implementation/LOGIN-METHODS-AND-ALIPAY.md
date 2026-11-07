# 登录方式选择与支付宝接入

## 按 App 选择登录方式

App 的配置页面分别控制密码、邮箱验证码、短信验证码和第三方登录；每个第三方连接有独立的 active/disabled 状态。App 策略和连接状态由服务端强制检查，不能通过直接提交隐藏表单绕过。

例如只允许微信：

1. 在连接中配置审核通过的微信开放平台网站应用，启用这个连接，禁用其他不允许的连接。
2. 关闭密码、邮箱验证码和短信验证码登录，开启第三方登录。
3. 如果首次微信登录需要创建本地用户，保留「允许新用户注册」和「第三方首次登录创建用户」；关闭前者会禁止所有新用户，包括第三方新用户。
4. 填写操作原因并保存。预设只修改草稿，不会自动保存，也不会替你选择供应商或验证供应商凭据。

仅允许微信和支付宝时，在第一步保留这两个连接即可。配置隔离到当前 App，另一 App 的账号、策略和连接独立。

关闭所有业务登录方式可以用于维护，但必须明确提示不可登录。固定的系统管理客户端继续要求密码与 MFA；平台运营账号继续走平台邮箱、密码、TOTP 登录，业务策略不改变管理权限。

## 支付宝原生网页授权

本适配面向中国大陆支付宝开放平台网页 `auth_user` 授权、公钥模式、RSA2。不是支付宝支付、Alipay+、小程序 SDK 或证书模式。需要已开通相应网页授权和用户信息权限的应用，并配置合法的授权回调域名。

- 浏览器授权：`https://openauth.alipay.com/oauth2/publicAppAuthorize.htm`
- 服务端固定网关：`https://openapi.alipay.com/gateway.do`
- 先调用 `alipay.system.oauth.token` 换取临时令牌，再调用 `alipay.user.info.share` 获取签名身份。
- 回调使用 `auth_code`；必须带匹配的 `app_id`，不能用普通 OAuth 的 `code` 参数代替。
- 以两个接口一致的 `open_id` 作为外部主体。不会回退到 `user_id` 或使用 `union_id` 自动合并身份；仅返回旧 user_id 的应用需要先完成上游 OpenID 升级。
- 不将上游昵称、邮箱、手机号当作已验证凭据；当前仅映射稳定主体与昵称。

### 配置接口

`POST /api/v1/apps/{app_id}/connections`：

```json
{
  "name": "支付宝",
  "provider_type": "alipay",
  "client_id": "你的支付宝 AppID",
  "alipay_credentials": {
    "private_key": "应用 RSA 私钥",
    "alipay_public_key": "支付宝 RSA 公钥"
  }
}
```

支付宝使用 `alipay_credentials`，不能同时提供 `client_secret`。两把密钥均支持 PEM 或 base64 DER；应用私钥支持 PKCS#1/PKCS#8，支付宝公钥支持 SubjectPublicKeyInfo。RSA 长度要求 2048～4096 位，拒绝加密 PEM、证书、相同的应用/平台密钥和不完整密钥对。AppID、连接类型和 issuer 创建后不可更改。

`PATCH /api/v1/apps/{app_id}/connections/{connection_id}` 更新密钥时仍带 `config_version` 和 `reason`，并完整提交 `alipay_credentials` 两把密钥，原子替换；省略整个对象则保留原密钥。部分密钥、空串、过期版本被拒绝。更改密钥或启停状态会改变连接版本，使已开始的旧版本回调失效。

读取和更新响应均不返回密钥。密钥对复用已有的加密连接存储，PostgreSQL/MySQL 不增加供应商专属表。日志和审计不记录私钥、令牌或完整上游响应。

### 协议安全

- 请求使用 RSA2（SHA-256 + RSA PKCS#1 v1.5），按键名排序后对未 URL 编码的非空参数签名；签名后的表单只 POST 到固定网关，不跟随重定向。
- 响应对原始 JSON 对象字节验签后再读取身份；不重新序列化后验签。
- 检查错误响应、业务错误码、响应大小、身份一致性；上游 token 仅在当前请求中使用。
- 继承 App 隔离、flow cookie/CSRF、一次性 state、连接版本、实时策略、绑定冲突与 MFA 检查。回调前关闭第三方策略时，禁止执行上游请求；证明接收时再次校验。

## 本地验收与边界

新增原始 JSON 验签、请求规范串、PKCS#1/PKCS#8、无签名/篡改/错误公钥/身份替换/超大响应/重定向等单测；数据库集成覆盖新建、密钥不回读、完整更新、过期版本、回调 AppID、auth_code、state 重放及关闭策略等行为。使用独立测试库和本地生成的签名协议夹具，不使用真实支付宝账户。

本地通过不代表已完成真实支付宝联调。正式接入仍需用部署方审核通过的真实应用和回调域名验证首次授权、已有绑定登录、取消授权及密钥更新。真实凭据不得提交到 Git。

## 官方依据

- [支付宝网页授权流程（官方文档镜像）](https://developer.alibaba.com/docs/doc.htm?articleId=105809&docType=1&treeId=193)
- [支付宝换取授权令牌](https://iopenhome.alipay.com/docs/ac/solution_api/oauth_token)
- [支付宝用户信息](https://iopenhome.alipay.com/docs/ac/solution_api/infoshare)
- [官方 Python SDK 的 OpenID 返回结构](https://github.com/alipay/alipay-sdk-python-all/blob/master/alipay/aop/api/response/AlipaySystemOauthTokenResponse.py)
- [官方 SDK 签名/响应验签实现](https://github.com/alipay/alipay-sdk-python-all/blob/master/alipay/aop/api/DefaultAlipayClient.py)

资料核对日期：2026-10-15。部分 opendocs 页面当前无法提取正文；同时核对官方 SDK 与官方镜像，真实供应商行为仍以真实接入验收为准。
