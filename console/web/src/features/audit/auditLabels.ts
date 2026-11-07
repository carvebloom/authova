/**
 * 审计动作字典：已知 action 映射为中文说明，未知动作原样展示（服务端为最终权威）。
 * 词条与 Core 审计写入点一一对应（见 authova/internal 的 audit 调用）。
 */
const ACTION_LABELS: Record<string, string> = {
  'app.create': '创建应用',
  'app.update': '更新应用配置',
  'app.authentication_policy_update': '更新登录策略',
  'app.suspend': '暂停应用',
  'app.resume': '恢复应用',
  'key.rotate': '轮换应用密钥',
  'key.revoke': '紧急吊销签名密钥',
  'client.create': '创建客户端',
  'client.update': '更新客户端',
  'client.secret_rotate': '轮换客户端密钥',
  'client.previous_secret_revoke': '撤销旧客户端密钥',
  'client.management_provision': '初始化内置客户端',
  'user.disable': '禁用用户',
  'user.enable': '恢复已禁用用户',
  'user.register': '用户注册',
  'user.authenticate': '用户登录',
  'user.mfa': '用户完成 MFA',
  'user.mfa_enroll': '用户绑定 MFA',
  'user.mfa_replace': '用户更换认证器',
  'user.mfa_recovery': '用户使用恢复码',
  'user.recovery_codes_generate': '生成恢复码',
  'user.password_change': '用户修改密码',
  'user.password_reset': '用户重置密码',
  'user.identifier_change': '用户改绑邮箱/手机号',
  'user.erase': '用户注销并删除数据',
  'user.external_link': '绑定外部身份',
  'user.external_unlink': '解绑外部身份',
  'user.federation': '外部身份登录',
  'admin.grant': '变更 App 管理员角色',
  'session.create': '创建会话',
  'session.revoke': '撤销会话',
  'session.logout': '用户退出登录',
  'connection.create': '创建连接',
  'connection.update': '更新连接',
  'refresh.reuse': '检测到刷新令牌重用',
  'platform.operator_create': '创建运营账号',
  'platform.operator_update': '变更运营账号',
  'platform.operator_recovered': '恢复运营账号 TOTP',
  'platform.login': '平台账号登录',
  'platform.logout': '平台账号退出',
  'platform.bootstrap': '平台初始化引导',
}

/** 已知动作返回中文说明；未知动作返回 null，由调用方原样展示原始 action。 */
export function auditActionLabel(action: string): string | null {
  return ACTION_LABELS[action] ?? null
}
