import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode, useState } from 'react'
import { Dialog } from './Dialog'

function renderDialog({
  dismissible = true,
}: {
  dismissible?: boolean
} = {}) {
  const onClose = vi.fn()
  const utils = render(
    <div>
      <button type="button">触发按钮</button>
      <Dialog open title="测试对话框" onClose={onClose} dismissible={dismissible}>
        <button type="button">第一个</button>
        <button type="button">第二个</button>
      </Dialog>
    </div>,
  )
  return { onClose, ...utils }
}

afterEach(() => {
  // 兜底：确保滚动锁不跨测试泄漏
  document.body.style.overflow = ''
  document.getElementById('root')?.remove()
})

describe('Dialog 可访问行为', () => {
  it('restores the trigger after an autofocus form closes in StrictMode', async () => {
    const user = userEvent.setup()
    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <>
          <button onClick={() => setOpen(true)}>打开表单</button>
          <Dialog open={open} title="自动聚焦表单" onClose={() => setOpen(false)}>
            <input autoFocus aria-label="名称" />
          </Dialog>
        </>
      )
    }
    render(
      <StrictMode>
        <Harness />
      </StrictMode>,
    )
    const trigger = screen.getByRole('button', { name: '打开表单' })
    await user.click(trigger)
    await user.keyboard('{Escape}')
    expect(document.activeElement).toBe(trigger)
  })
  it('does not trap keyboard focus on CSS-hidden or negative-tabindex controls', async () => {
    const user = userEvent.setup()
    render(
      <Dialog open title="可见控件" onClose={() => {}} dismissible={false}>
        <button>可见</button>
        <button style={{ display: 'none' }}>隐藏</button>
        <button tabIndex={-1}>程序焦点</button>
      </Dialog>,
    )
    const visible = screen.getByRole('button', { name: '可见' })
    visible.focus()
    await user.tab()
    expect(document.activeElement).toBe(visible)
  })
  it('Esc 与遮罩点击关闭（dismissible 默认）', async () => {
    const user = userEvent.setup()
    const { onClose } = renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)

    onClose.mockClear()
    const overlay = document.querySelector('.dialog-overlay')!
    fireEvent.mouseDown(overlay)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('dismissible=false：Esc/遮罩不关闭，且不渲染关闭按钮（提交中禁止取消）', async () => {
    const user = userEvent.setup()
    const { onClose } = renderDialog({ dismissible: false })
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '关闭' })).not.toBeInTheDocument()

    await user.keyboard('{Escape}')
    const overlay = document.querySelector('.dialog-overlay')!
    fireEvent.mouseDown(overlay)
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('焦点陷阱：Tab 在面板内循环，不逃逸到背景', async () => {
    const user = userEvent.setup()
    renderDialog()
    const dialog = screen.getByRole('dialog')
    const first = screen.getByRole('button', { name: '关闭' })
    const second = screen.getByRole('button', { name: '第一个' })
    const last = screen.getByRole('button', { name: '第二个' })

    // 打开时焦点进入面板
    expect(dialog.contains(document.activeElement)).toBe(true)

    last.focus()
    await user.tab()
    expect(document.activeElement).toBe(first)

    first.focus()
    await user.tab({ shift: true })
    expect(document.activeElement).toBe(last)

    // 正常顺序不受阻：从首位前进到第二位
    first.focus()
    await user.tab()
    expect(document.activeElement).toBe(second)
  })

  it('打开时锁定背景滚动并标记 inert；关闭后恢复并把焦点还给触发元素', async () => {
    const user = userEvent.setup()
    const root = document.createElement('div')
    root.id = 'root'
    document.body.appendChild(root)

    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <div>
          <button type="button" onClick={() => setOpen(true)}>
            打开
          </button>
          <Dialog open={open} title="可关闭" onClose={() => setOpen(false)}>
            <button type="button">内部按钮</button>
          </Dialog>
        </div>
      )
    }
    render(<Harness />)

    const trigger = screen.getByRole('button', { name: '打开' })
    await user.click(trigger)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(document.body.style.overflow).toBe('hidden')
    expect(root.hasAttribute('inert')).toBe(true)
    // 焦点进入对话框
    expect(document.activeElement).not.toBe(trigger)
    expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true)

    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(document.body.style.overflow).toBe('')
    expect(root.hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(trigger)
  })

  it('带 autoFocus 的表单：打开时焦点落在输入框，关闭后仍恢复到触发按钮', async () => {
    const user = userEvent.setup()
    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <div>
          <button type="button" onClick={() => setOpen(true)}>
            打开表单
          </button>
          <Dialog open={open} title="带表单" onClose={() => setOpen(false)}>
            <input autoFocus aria-label="名称输入" />
            <button type="button">确认</button>
          </Dialog>
        </div>
      )
    }
    render(<Harness />)
    const trigger = screen.getByRole('button', { name: '打开表单' })
    await user.click(trigger)
    // autoFocus 生效：焦点在输入框而非面板
    expect(screen.getByLabelText('名称输入')).toHaveFocus()
    await user.keyboard('{Escape}')
    // 恢复的是真正的触发按钮，而不是被 autoFocus 篡改的输入框
    expect(trigger).toHaveFocus()
  })

  it('焦点停在面板自身时 Shift+Tab 也陷在模态内', async () => {
    const user = userEvent.setup()
    renderDialog()
    const dialog = screen.getByRole('dialog')
    // 初始焦点在面板自身
    expect(document.activeElement).toBe(dialog)
    await user.tab({ shift: true })
    // 反向循环到面板内最后一个可聚焦元素，而非逃逸到背景
    expect(document.activeElement).toBe(screen.getByRole('button', { name: '第二个' }))
  })

  it('多模态并发（防御）：Esc 只作用于最顶层，不会两层全关', async () => {
    const user = userEvent.setup()
    const closeA = vi.fn()
    const closeB = vi.fn()
    render(
      <div>
        <Dialog open title="先开的" onClose={closeA}>
          <button type="button">A 按钮</button>
        </Dialog>
        <Dialog open title="后开的" onClose={closeB}>
          <button type="button">B 按钮</button>
        </Dialog>
      </div>,
    )
    expect(screen.getAllByRole('dialog')).toHaveLength(2)
    await user.keyboard('{Escape}')
    // 仅最顶层（DOM 靠后、视觉在最上的后者）响应
    expect(closeB).toHaveBeenCalledTimes(1)
    expect(closeA).not.toHaveBeenCalled()
  })
})
