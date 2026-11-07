import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '../../api/client'
import { ReasonDialog } from './ReasonDialog'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ReasonDialog', () => {
  it('原因必填：空提交展示字段错误且不调用 onSubmit', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn(async () => {})
    render(
      <ReasonDialog
        open
        title="暂停应用"
        confirmLabel="确认暂停"
        danger
        onClose={() => {}}
        onSubmit={onSubmit}
      />,
    )
    await user.click(screen.getByRole('button', { name: '确认暂停' }))
    expect(await screen.findByText('请填写操作原因')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('提交成功传入去除空白的原因并关闭对话框', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn(async () => {})
    const onClose = vi.fn()
    render(
      <ReasonDialog
        open
        title="撤销会话"
        confirmLabel="确认撤销"
        onClose={onClose}
        onSubmit={onSubmit}
      />,
    )
    await user.type(screen.getByLabelText('操作原因'), '  安全事件响应  ')
    await user.click(screen.getByRole('button', { name: '确认撤销' }))
    expect(onSubmit).toHaveBeenCalledWith('安全事件响应')
    expect(onClose).toHaveBeenCalled()
  })

  it('onSubmit 抛错时展示服务端错误且保持打开', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const onSubmit = vi.fn(async () => {
      throw new ApiError({
        status: 409,
        title: 'Conflict',
        detail: '应用已处于暂停状态',
        code: 'conflict',
        requestId: 'req-409',
      })
    })
    render(
      <ReasonDialog
        open
        title="暂停应用"
        confirmLabel="确认暂停"
        onClose={onClose}
        onSubmit={onSubmit}
      />,
    )
    await user.type(screen.getByLabelText('操作原因'), '重复操作')
    await user.click(screen.getByRole('button', { name: '确认暂停' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('应用已处于暂停状态')
    expect(alert).toHaveTextContent('req-409')
    expect(onClose).not.toHaveBeenCalled()
  })
})
