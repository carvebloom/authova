import { Component, type ErrorInfo, type ReactNode } from 'react'

export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  componentDidCatch(_error: Error, _info: ErrorInfo) {
    // Error objects may contain user input or a one-time secret. Never log them.
  }
  render() {
    if (!this.state.failed) return this.props.children
    return (
      <main className="fullscreen-error" role="alert">
        <h1>页面暂时无法显示</h1>
        <p>请重新加载页面。此前提交的操作可能已经生效，请先核对状态。</p>
        <button type="button" className="btn btn-primary" onClick={() => window.location.reload()}>
          重新加载页面
        </button>
      </main>
    )
  }
}
