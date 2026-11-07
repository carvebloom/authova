import { useState } from 'react'
import * as api from '../../api/endpoints'
import type { AuditEvent } from '../../api/types'
import { usePagedList } from '../../shared/hooks/usePagedList'
import { useAppContext } from '../../app/appContext'
import { Button, IconButton } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { CopyButton } from '../../shared/components/CopyButton'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { EmptyState, Loading } from '../../shared/components/States'
import { LoadMore } from '../../shared/components/LoadMore'
import { PageHeader } from '../../shared/components/PageHeader'
import { formatTime, shortenId } from '../../shared/utils/format'
import { auditActionLabel } from './auditLabels'

/**
 * 审计日志：中文动作说明（附原 action），详情对话框含 reason/actor/subject 与复制。
 * 游标分页浏览；契约无服务端过滤，不做“当前页过滤冒充全量检索”。
 */
export function AuditTab() {
  const { app } = useAppContext()
  const list = usePagedList(`audit:${app.id}`, (cursor) =>
    api.listAudit(app.id, { cursor, limit: 20 }),
  )
  const [detail, setDetail] = useState<AuditEvent | null>(null)

  return (
    <div className="tab-panel">
      <PageHeader
        level={2}
        title="审计日志"
        sub="该应用上的管理操作记录；倒序游标浏览。"
        actions={<IconButton icon="refresh" label="刷新列表" onClick={list.reload} />}
      />

      {list.loading ? (
        <Loading />
      ) : list.error ? (
        <ErrorNotice error={list.error} onRetry={list.reload} />
      ) : list.items.length === 0 ? (
        <EmptyState title="暂无审计记录" />
      ) : (
        <>
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">时间</th>
                  <th scope="col">操作</th>
                  <th scope="col">操作者</th>
                  <th scope="col">对象</th>
                  <th scope="col">
                    <span className="visually-hidden">详情</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((event) => {
                  const label = auditActionLabel(event.action)
                  return (
                    <tr key={event.id}>
                      <td className="nowrap">{formatTime(event.created_at)}</td>
                      <td>
                        <span className="audit-action">
                          {label ?? event.action}
                          {label ? (
                            <span className="audit-action-raw mono">{event.action}</span>
                          ) : null}
                        </span>
                      </td>
                      <td className="mono" title={event.actor_id}>
                        {shortenId(event.actor_id)}
                      </td>
                      <td className="mono" title={event.subject_id}>
                        {shortenId(event.subject_id)}
                      </td>
                      <td className="table-actions">
                        <Button size="sm" variant="ghost" onClick={() => setDetail(event)}>
                          详情
                        </Button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          {list.loadMoreError ? <ErrorNotice error={list.loadMoreError} /> : null}
          <LoadMore
            nextCursor={list.nextCursor}
            loading={list.loadingMore}
            onLoadMore={list.loadMore}
          />
        </>
      )}

      <AuditDetailDialog event={detail} onClose={() => setDetail(null)} />

      <p className="field-hint section-gap">
        此处仅为管理操作审计。结构化认证失败安全事件由 Core 以有界 JSON
        日志输出（含丢弃计数），请在部署侧日志系统查看，不并入本视图。
      </p>
    </div>
  )
}

function AuditDetailDialog({ event, onClose }: { event: AuditEvent | null; onClose: () => void }) {
  return (
    <Dialog open={event !== null} title="审计详情" onClose={onClose}>
      {event ? (
        <dl className="audit-detail-grid">
          <div className="audit-detail-row">
            <dt>操作</dt>
            <dd>
              <span>
                {auditActionLabel(event.action) ?? event.action}
                {auditActionLabel(event.action) ? (
                  <span className="audit-action-raw mono">（{event.action}）</span>
                ) : null}
              </span>
            </dd>
          </div>
          <div className="audit-detail-row">
            <dt>时间</dt>
            <dd>{formatTime(event.created_at)}</dd>
          </div>
          {event.reason ? (
            <div className="audit-detail-row">
              <dt>操作原因</dt>
              <dd>{event.reason}</dd>
            </div>
          ) : null}
          <div className="audit-detail-row">
            <dt>操作者</dt>
            <dd className="mono">
              {event.actor_id} <CopyButton value={event.actor_id} label="复制" />
            </dd>
          </div>
          <div className="audit-detail-row">
            <dt>对象</dt>
            <dd className="mono">
              {event.subject_id} <CopyButton value={event.subject_id} label="复制" />
            </dd>
          </div>
          <div className="audit-detail-row">
            <dt>事件 ID</dt>
            <dd className="mono">
              {event.id} <CopyButton value={event.id} label="复制" />
            </dd>
          </div>
        </dl>
      ) : null}
    </Dialog>
  )
}
