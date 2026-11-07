import type { ConnectionProviderType } from '../../api/types'

/**
 * 提供方展示元信息。仅覆盖网站扫码/网页授权形态：微信为开放平台审核通过的网站应用（AppID/AppSecret），
 * QQ 为 QQ 互联（QQ Connect）网站应用（AppID/AppKey），支付宝为开放平台网页应用（AppID + RSA2 密钥对，
 * 公钥模式，非证书、非支付）；不涉及移动 SDK、小程序或公众号。
 */
export interface ProviderInfo {
  label: string
  /** 服务端固定的 issuer，创建时不采集也不随请求提交；OIDC 须由用户填写规范的 HTTPS issuer */
  fixedIssuer?: string
  /** Client ID 字段提示（微信/QQ/支付宝对应上游网站/网页应用的 AppID） */
  clientIdHint?: string
  /** Client Secret 字段提示（微信对应 AppSecret，QQ 对应 AppKey）；支付宝无 client_secret */
  clientSecretHint?: string
  /** 凭据形态：client_secret 单密钥，或支付宝 RSA2 密钥对（alipay_credentials） */
  credentials: 'client_secret' | 'alipay_keypair'
}

export const PROVIDER_INFO: Record<ConnectionProviderType, ProviderInfo> = {
  oidc: {
    label: '标准 OIDC',
    clientSecretHint: '仅用于创建请求，之后的读取响应不会包含该字段',
    credentials: 'client_secret',
  },
  github: {
    label: 'GitHub',
    fixedIssuer: 'https://github.com',
    clientIdHint: 'GitHub.com OAuth App 的 Client ID',
    clientSecretHint:
      'GitHub.com OAuth App 的 Client Secret；仅用于创建请求，之后的读取响应不会包含该字段',
    credentials: 'client_secret',
  },
  wechat: {
    label: '微信网站扫码',
    fixedIssuer: 'https://open.weixin.qq.com',
    clientIdHint: '微信开放平台审核通过的网站应用的 AppID',
    clientSecretHint: '对应网站应用的 AppSecret；仅用于创建请求，之后的读取响应不会包含该字段',
    credentials: 'client_secret',
  },
  qq: {
    label: 'QQ 网站登录',
    fixedIssuer: 'https://graph.qq.com',
    clientIdHint: 'QQ 互联（QQ Connect）审核通过的网站应用的 AppID',
    clientSecretHint: '对应网站应用的 AppKey；仅用于创建请求，之后的读取响应不会包含该字段',
    credentials: 'client_secret',
  },
  alipay: {
    label: '支付宝网页授权',
    fixedIssuer: 'https://openauth.alipay.com',
    clientIdHint: '支付宝开放平台网页应用的 AppID（纯数字）',
    credentials: 'alipay_keypair',
  },
}

/** 已知提供方映射为惯用名；未知值原样展示（服务端为最终权威） */
export function providerLabel(providerType: string): string {
  if (providerType === 'oidc') return PROVIDER_INFO.oidc.label
  if (providerType === 'github') return PROVIDER_INFO.github.label
  if (providerType === 'wechat') return PROVIDER_INFO.wechat.label
  if (providerType === 'qq') return PROVIDER_INFO.qq.label
  if (providerType === 'alipay') return PROVIDER_INFO.alipay.label
  return providerType
}
