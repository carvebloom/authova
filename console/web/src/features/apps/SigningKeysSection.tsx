import { useState } from 'react'
import * as api from '../../api/endpoints'
import type { SigningKey } from '../../api/types'
import type { AsyncState } from '../../shared/hooks/useAsync'
import { Button, IconButton } from '../../shared/components/Button'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { ReasonDialog } from '../../shared/components/ReasonDialog'
import { EmptyState, Loading } from '../../shared/components/States'
import { formatUnixSeconds, shortenId } from '../../shared/utils/format'

interface KeyStatus {
  label: string
  tone: 'ok' | 'warn' | 'bad' | 'muted'
}

/** 签名密钥展示状态：当前签名 / 验签保留中 / 已退役 / 已吊销；未知形态原样降级为“非当前” */
function keyStatus(key: SigningKey, nowMs: number = Date.now()): KeyStatus {
  if (key.revoked_at > 0) return { label: '已吊销', tone: 'bad' }
  if (key.current) return { label: '当前签名', tone: 'ok' }
  if (key.retire_at * 1000 > nowMs) {
    return { label: `验签保留至 ${formatUnixSeconds(key.retire_at)}`, tone: 'warn' }
  }
  if (key.retire_at > 0) return { label: '已退役', tone: 'muted' }
  return { label: '非当前', tone: 'muted' }
}

/**
 * 签名密钥列表与紧急吊销：
 * - 常规轮换在「危险操作」区：旧验证密钥保留 24 小时，不是立即失效；
 * - 紧急吊销立即从 JWKS 移除指定密钥（吊销当前签名密钥时服务端生成继任者）；
 * - 只展示密钥元数据（kid/时间/状态），私钥材料不经由控制台；
 * - 吊销携带最近一次 GET /keys 的 config_version；409/结果未知冻结，GET 读回核对后再决定。
 */
export function SigningKeysSection({
  appId,
  canRotateKeys,
  keys,
}: {
  appId: string
  canRotateKeys: boolean
  keys: AsyncState<{ items: SigningKey[]; config_version: number }>
}) {
  const [revokingId, setRevokingId] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  // 对象本体始终从最新列表取：核对读回后 copy 与版本随之更新
  const revoking = revokingId
    ? (keys.data?.items.find((k) => k.key_id === revokingId) ?? null)
    : null

  return (
    <section className="panel" aria-label="签名密钥">
      <div className="panel-head-row">
        <h3 className="panel-title">签名密钥</h3>
        <IconButton icon="refresh" label="刷新密钥列表" onClick={keys.reload} />
      </div>
      <p className="field-hint">
        常规轮换保留旧验证密钥 24 小时；紧急吊销立即从 JWKS
        移除指定密钥。此处只展示密钥元数据，私钥材料不经由控制台。
      </p>

      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}

      {keys.loading ? (
        <Loading label="正在加载签名密钥…" />
      ) : keys.error ? (
        <ErrorNotice error={keys.error} onRetry={keys.reload} />
      ) : !keys.data || keys.data.items.length === 0 ? (
        <EmptyState title="暂无签名密钥记录" />
      ) : (
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">密钥 ID（kid）</th>
                <th scope="col">状态</th>
                <th scope="col">创建时间</th>
                <th scope="col">验签保留至</th>
                <th scope="col">吊销时间</th>
                {canRotateKeys ? (
                  <th scope="col">
                    <span className="visually-hidden">操作</span>
                  </th>
                ) : null}
              </tr>
            </thead>
            <tbody>
              {keys.data.items.map((key) => {
                const status = keyStatus(key)
                return (
                  <tr key={key.key_id}>
                    <td className="mono" title={key.key_id}>
                      {shortenId(key.key_id)}
                    </td>
                    <td>
                      <span className={`badge badge-${status.tone}`}>{status.label}</span>
                    </td>
                    <td className="nowrap">{formatUnixSeconds(key.created_at)}</td>
                    <td className="nowrap">{formatUnixSeconds(key.retire_at)}</td>
                    <td className="nowrap">{formatUnixSeconds(key.revoked_at)}</td>
                    {canRotateKeys ? (
                      <td className="table-actions">
                        {key.revoked_at === 0 ? (
                          <Button
                            size="sm"
                            variant="danger"
                            onClick={() => {
                              setNotice(null)
                              setRevokingId(key.key_id)
                            }}
                          >
                            紧急吊销
                          </Button>
                        ) : null}
                      </td>
                    ) : null}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      <ReasonDialog
        open={revoking !== null}
        title="紧急吊销签名密钥"
        description={
          revoking
            ? `将立即把密钥 ${revoking.key_id} 从 JWKS 移除，服务端在线验签即刻使用已提交状态。${
                revoking.current
                  ? '该密钥是当前签名密钥：吊销后服务端同时生成新的签名密钥，此后签发的令牌由新密钥签名。'
                  : ''
              }注意：外部离线缓存了 JWKS 的 JWT 校验方必须自行刷新或拒绝旧 kid，无法对其承诺立即生效。`
            : undefined
        }
        confirmLabel="确认紧急吊销"
        danger
        onClose={() => setRevokingId(null)}
        onReconcile={keys.reload}
        onSubmit={async (reason) => {
          if (!revoking || !keys.data) return
          const result = await api.revokeSigningKey(appId, revoking.key_id, {
            config_version: keys.data.config_version,
            reason,
          })
          setNotice(
            result.new_key_id
              ? `已吊销密钥 ${shortenId(result.key_id)}，新签名密钥 ${shortenId(result.new_key_id)} 已启用。`
              : `已吊销密钥 ${shortenId(result.key_id)}。`,
          )
          setRevokingId(null)
          keys.reload()
        }}
      />
    </section>
  )
}
