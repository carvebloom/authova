import { Button } from './Button'

export interface LoadMoreProps {
  nextCursor: string | null
  loading: boolean
  onLoadMore: () => void
}

/** 游标分页：有 next_cursor 才展示“加载更多”；加载中互斥由 usePagedList 保证。 */
export function LoadMore({ nextCursor, loading, onLoadMore }: LoadMoreProps) {
  if (!nextCursor) return null
  return (
    <div className="load-more">
      <Button onClick={onLoadMore} loading={loading}>
        加载更多
      </Button>
    </div>
  )
}
