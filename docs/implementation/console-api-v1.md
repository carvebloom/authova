# Console 实施接口 v1

控制台 UI 只调用同源 Go BFF `/bff`；BFF 调用核心 `/api/v1`。JSON snake_case。所有列表为 `{items: [...], next_cursor: string|null}`。错误 `{type,title,status,detail,code,request_id}`。ID 为不透明字符串。时间 RFC3339 UTC。未实现能力不显示可点击入口。

- `POST /bff/login` `{email,password,totp}` -> `{operator:{id,email,role},csrf_token}`；BFF 保管核心管理 token，Cookie HttpOnly，前端不存 token。
- `GET /bff/me` -> 同上；`POST /bff/logout` 清理双方会话。所有写操作带 `X-CSRF-Token`。
- `GET /bff/apps`；`POST /bff/apps` `{name,slug}`；`GET/PATCH /bff/apps/{id}`。App `{id,app_key,name,slug,status,issuer,created_at,config_version}`。
- `POST /bff/apps/{id}/suspend|resume` `{reason}`。
- `GET/POST /bff/apps/{id}/clients`；Client 输入 `{name,kind:"public"|"confidential",redirect_uris,allowed_scopes,allowed_audiences,allowed_introspection_audiences,grant_types}`。返回 `{id,client_id,app_id,name,kind,status,...}`；创建 confidential 返回一次性 `client_secret`，UI 显式提示并有复制操作。GET/PATCH `/clients/{client_id}`。
- `GET /bff/apps/{id}/users?cursor=&limit=&q=`；User `{id,app_id,display_name,status,email,phone,created_at,session_epoch}`，联系方式脱敏。
- `GET /bff/apps/{id}/users/{user_id}`；`POST .../disable` `{reason}`；`GET .../sessions`；`DELETE .../sessions/{session_id}` `{reason}`。Session `{id,created_at,expires_at,status,amr}`。
- `GET /bff/apps/{id}/audit`；审计 `{id,app_id,actor_id,action,subject_id,created_at}`。
- `GET/POST /bff/apps/{id}/connections`；OIDC 连接 `{id,name,issuer,client_id,status}`；创建输入另含 `client_secret`，读响应绝不含 Secret。
- `POST /bff/apps/{id}/keys/rotate` `{reason}`。

核心对应路径以 `/api/v1` 替换 `/bff`；平台会话路径 `/api/v1/platform/sessions`、当前主体 `/api/v1/platform/me`。BFF 不访问数据库，也不改变错误/状态以制造成功。

## 已实现补充

- `GET/POST /bff/platform/operators`；owner 创建返回一次性 TOTP secret，读取不返回凭据。`PATCH /bff/platform/operators/{id}` 修改固定角色/状态并要求 reason，禁止失去最后一个平台 owner。
- `POST /bff/app-login` `{app_key}` 返回当前 App 管理 Client 的 PKCE 入口；`GET /app/callback` 在 BFF 验证 nonce、at_hash 并保存加密会话。App 管理身份无平台 owner 权限。
- Client/App 修改必须带 `config_version`；用户 q 是完整 email/phone 的精确查询。审计响应可含 reason。MFA 和管理 audience 由 Core 逐请求核验。

## UI 布局

Authova 品牌、平静浅色工作台、单一深绿色强调色；左侧 App 导航，主区当前 App、状态、用户/Client/连接/审计表格和操作。桌面与 375px 可用；表单有加载、字段错误、失败、空列表和秘密只展示一次状态；禁止占位统计数字和 mock 成功。Vite + React + strict TypeScript UI 与 Go BFF 均已实现，真实流程证据见 STATUS；窄屏表格在容器内横向滚动。
