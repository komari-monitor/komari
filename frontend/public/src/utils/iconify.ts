/**
 * Iconify 集合预注册（可选）
 *
 * 默认行为：`<Icon icon="icon-park-outline:sun" />` 在未注册集合时
 * 会从 https://api.iconify.design 按需拉取单个图标 SVG（带浏览器缓存）。
 *
 * 此函数保留作为未来扩展入口；当前不做预注册，避免把整个
 * 图标集合（每个 1MB+）打进首屏 bundle。
 */
export async function setupIconify(): Promise<void> {
  // no-op：交给 @iconify/vue 默认 CDN 加载策略
}
