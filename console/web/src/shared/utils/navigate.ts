/** 整页跳转的唯一出口；测试中可替换以断言导航行为。 */
export function assignLocation(url: string): void {
  window.location.assign(url)
}
