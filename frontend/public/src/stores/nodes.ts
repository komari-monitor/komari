import type { Client, NodeStatus } from '@/utils/rpc'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useAppStore } from '@/stores/app'
import { gpuUsageFromStatus } from '@/utils/gpuHelper'
import { parseNodeGroups } from '@/utils/groupHelper'

/** 流量限制类型 */
export type TrafficLimitType = 'up' | 'down' | 'min' | 'max' | 'sum'

/** 节点完整信息（合并 Client 和 Status） */
export interface NodeData {
  uuid: string
  // Client 信息
  name: string
  cpu_name: string
  virtualization: string
  arch: string
  cpu_cores: number
  cpu_physical_cores?: number
  os: string
  kernel_version: string
  gpu_name?: string
  ipv4?: string
  ipv6?: string
  region: string
  remark?: string
  public_remark: string
  mem_total: number
  swap_total: number
  disk_total: number
  version?: string
  weight: number
  price: number
  billing_cycle: number
  auto_renewal: boolean
  currency: string
  expired_at: string
  group: string
  groups: string[]
  tags: string
  hidden: boolean
  traffic_limit: number
  traffic_limit_type: TrafficLimitType
  traffic_reset_day: number
  traffic_reset_time: string
  traffic_reset_timezone: string
  created_at: string
  updated_at: string
  // Status 信息
  online: boolean
  time: string
  cpu: number
  gpu: number
  ram: number
  swap: number
  load: number
  load5: number
  load15: number
  temp: number
  disk: number
  net_in: number
  net_out: number
  net_total_up: number
  net_total_down: number
  traffic_up?: number
  traffic_down?: number
  traffic_cycle_up?: number
  traffic_cycle_down?: number
  traffic_cycle_start?: string
  traffic_cycle_next_reset?: string
  traffic_cycle_ready?: boolean
  process: number
  connections: number
  connections_udp: number
  uptime: number
  message?: string
  status_updated_at?: string
}

/** WebSocket 连接状态 */
export type WsConnectionState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting'

/** 状态数据（用于更新） */
interface StatusData {
  online: boolean
  time: string
  cpu: number
  gpu: number
  ram: number
  swap: number
  load: number
  load5: number
  load15: number
  temp: number
  disk: number
  net_in: number
  net_out: number
  net_total_up: number
  net_total_down: number
  traffic_up?: number
  traffic_down?: number
  process: number
  connections: number
  connections_udp: number
  uptime: number
  message?: string
  updated_at?: string
}

const useNodesStore = defineStore('nodes', () => {
  // ===== 状态 =====
  const appStore = useAppStore()
  const nodes = ref<NodeData[]>([])
  const wsConnectionState = ref<WsConnectionState>('disconnected')
  const wsReconnectAttempts = ref<number>(0)

  // ===== 计算属性 =====
  const nodeIndex = new Map<string, NodeData>()

  /** 可见节点（未登录时 Komari hidden 节点不参与公开首页展示） */
  const visibleNodes = computed(() => appStore.privateFeaturesAllowed ? nodes.value : nodes.value.filter(n => !n.hidden))

  /** 在线节点数量 */
  const onlineCount = computed(() => visibleNodes.value.filter(n => n.online).length)

  /** 总节点数量 */
  const totalCount = computed(() => visibleNodes.value.length)

  /** 所有分组 */
  const groups = computed(() => {
    const groupSet = new Set<string>()
    visibleNodes.value.forEach((n) => {
      n.groups.forEach(group => groupSet.add(group))
    })
    return Array.from(groupSet)
  })

  function areStringArraysEqual(left: string[], right: string[]): boolean {
    return left.length === right.length && left.every((value, index) => value === right[index])
  }

  function addIndexedNode(node: NodeData): void {
    nodes.value.push(node)
    const reactiveNode = nodes.value.at(-1)
    if (reactiveNode)
      nodeIndex.set(reactiveNode.uuid, reactiveNode)
  }

  /** 按 UUID 索引的节点映射 */
  const nodesByUuid = computed(() => {
    const map = new Map<string, NodeData>()
    nodes.value.forEach((n) => {
      map.set(n.uuid, n)
    })
    return map
  })

  const visibleNodesByUuid = computed(() => {
    const map = new Map<string, NodeData>()
    visibleNodes.value.forEach((n) => {
      map.set(n.uuid, n)
    })
    return map
  })

  // ===== 方法 =====

  /**
   * 从 Client 对象创建节点数据。
   * 节点对象本身必须保持响应式，实时 CPU / 流量 / 在线状态依赖这些字段触发 UI 更新；
   * 若后续挂载复杂静态元数据，应在字段级使用 markRaw 或放入专用共享缓存。
   */
  function createNodeFromClient(client: Client): NodeData {
    return {
      uuid: client.uuid,
      name: client.name,
      cpu_name: client.cpu_name,
      virtualization: client.virtualization,
      arch: client.arch,
      cpu_cores: client.cpu_cores,
      cpu_physical_cores: client.cpu_physical_cores,
      os: client.os,
      kernel_version: client.kernel_version,
      gpu_name: client.gpu_name,
      ipv4: client.ipv4,
      ipv6: client.ipv6,
      region: client.region,
      remark: client.remark,
      public_remark: client.public_remark,
      mem_total: client.mem_total,
      swap_total: client.swap_total,
      disk_total: client.disk_total,
      version: client.version,
      weight: client.weight,
      price: client.price,
      billing_cycle: client.billing_cycle,
      auto_renewal: client.auto_renewal,
      currency: client.currency,
      expired_at: client.expired_at,
      group: client.group,
      groups: parseNodeGroups(client.group),
      tags: client.tags,
      hidden: client.hidden,
      traffic_limit: client.traffic_limit,
      traffic_limit_type: client.traffic_limit_type as TrafficLimitType,
      traffic_reset_day: client.traffic_reset_day ?? 0,
      traffic_reset_time: client.traffic_reset_time || '00:00',
      traffic_reset_timezone: client.traffic_reset_timezone || 'UTC',
      created_at: client.created_at,
      updated_at: client.updated_at,
      // Status 默认值
      online: false,
      time: '',
      cpu: 0,
      gpu: 0,
      ram: 0,
      swap: 0,
      load: 0,
      load5: 0,
      load15: 0,
      temp: 0,
      disk: 0,
      net_in: 0,
      net_out: 0,
      net_total_up: 0,
      net_total_down: 0,
      traffic_up: 0,
      traffic_down: 0,
      traffic_cycle_ready: false,
      process: 0,
      connections: 0,
      connections_udp: 0,
      uptime: 0,
    }
  }

  /**
   * 就地写入状态字段，仅在值实际变化时赋值。
   *
   * 关键：不要用 `{...node}` 生成新对象再整体替换数组元素——那会让引用改变，
   * Vue 会把整个节点视为「变了」，导致依赖它的所有组件（NodeCard / 列表行 /
   * 汇总卡片 / 地球）每轮轮询都整体重渲染。就地按字段变更后，Vue 的细粒度响应式
   * 只会重算真正变化的字段所对应的视图；未变化的字段连依赖都不会触发。
   */
  function applyStatus(node: NodeData, status: StatusData): void {
    if (node.online !== status.online)
      node.online = status.online
    if (node.time !== status.time)
      node.time = status.time
    if (node.cpu !== status.cpu)
      node.cpu = status.cpu
    const gpu = gpuUsageFromStatus(status)
    if (node.gpu !== gpu)
      node.gpu = gpu
    if (node.ram !== status.ram)
      node.ram = status.ram
    if (node.swap !== status.swap)
      node.swap = status.swap
    if (node.load !== status.load)
      node.load = status.load
    if (node.load5 !== status.load5)
      node.load5 = status.load5
    if (node.load15 !== status.load15)
      node.load15 = status.load15
    if (node.temp !== status.temp)
      node.temp = status.temp
    if (node.disk !== status.disk)
      node.disk = status.disk
    if (node.net_in !== status.net_in)
      node.net_in = status.net_in
    if (node.net_out !== status.net_out)
      node.net_out = status.net_out
    if (node.net_total_up !== status.net_total_up)
      node.net_total_up = status.net_total_up
    if (node.net_total_down !== status.net_total_down)
      node.net_total_down = status.net_total_down
    if (node.traffic_up !== status.traffic_up)
      node.traffic_up = status.traffic_up
    if (node.traffic_down !== status.traffic_down)
      node.traffic_down = status.traffic_down
    if (node.process !== status.process)
      node.process = status.process
    if (node.connections !== status.connections)
      node.connections = status.connections
    if (node.connections_udp !== status.connections_udp)
      node.connections_udp = status.connections_udp
    if (node.uptime !== status.uptime)
      node.uptime = status.uptime
    if (node.message !== status.message)
      node.message = status.message
    if (node.status_updated_at !== status.updated_at)
      node.status_updated_at = status.updated_at
  }

  /**
   * 就地写入 Client（元数据）字段，仅在值实际变化时赋值。
   *
   * 元数据极少变动，因此每轮轮询的 updateNodeClients 几乎全是空赋值，不触发任何
   * 重渲染。返回 weight 是否发生变化，调用方据此决定是否需要重新排序。
   */
  function applyClient(node: NodeData, client: Client): boolean {
    const weightChanged = node.weight !== client.weight
    if (node.name !== client.name)
      node.name = client.name
    if (node.cpu_name !== client.cpu_name)
      node.cpu_name = client.cpu_name
    if (node.virtualization !== client.virtualization)
      node.virtualization = client.virtualization
    if (node.arch !== client.arch)
      node.arch = client.arch
    if (node.cpu_cores !== client.cpu_cores)
      node.cpu_cores = client.cpu_cores
    if (node.cpu_physical_cores !== client.cpu_physical_cores)
      node.cpu_physical_cores = client.cpu_physical_cores
    if (node.os !== client.os)
      node.os = client.os
    if (node.kernel_version !== client.kernel_version)
      node.kernel_version = client.kernel_version
    if (node.gpu_name !== client.gpu_name)
      node.gpu_name = client.gpu_name
    if (node.ipv4 !== client.ipv4)
      node.ipv4 = client.ipv4
    if (node.ipv6 !== client.ipv6)
      node.ipv6 = client.ipv6
    if (node.region !== client.region)
      node.region = client.region
    if (node.remark !== client.remark)
      node.remark = client.remark
    if (node.public_remark !== client.public_remark)
      node.public_remark = client.public_remark
    if (node.mem_total !== client.mem_total)
      node.mem_total = client.mem_total
    if (node.swap_total !== client.swap_total)
      node.swap_total = client.swap_total
    if (node.disk_total !== client.disk_total)
      node.disk_total = client.disk_total
    if (node.version !== client.version)
      node.version = client.version
    if (weightChanged)
      node.weight = client.weight
    if (node.price !== client.price)
      node.price = client.price
    if (node.billing_cycle !== client.billing_cycle)
      node.billing_cycle = client.billing_cycle
    if (node.auto_renewal !== client.auto_renewal)
      node.auto_renewal = client.auto_renewal
    if (node.currency !== client.currency)
      node.currency = client.currency
    if (node.expired_at !== client.expired_at)
      node.expired_at = client.expired_at
    if (node.group !== client.group) {
      node.group = client.group
      const nextGroups = parseNodeGroups(client.group)
      if (!areStringArraysEqual(node.groups, nextGroups))
        node.groups = nextGroups
    }
    if (node.tags !== client.tags)
      node.tags = client.tags
    if (node.hidden !== client.hidden)
      node.hidden = client.hidden
    if (node.traffic_limit !== client.traffic_limit)
      node.traffic_limit = client.traffic_limit
    if (node.traffic_limit_type !== client.traffic_limit_type)
      node.traffic_limit_type = client.traffic_limit_type as TrafficLimitType
    if (node.traffic_reset_day !== client.traffic_reset_day)
      node.traffic_reset_day = client.traffic_reset_day ?? 0
    if (node.traffic_reset_time !== client.traffic_reset_time)
      node.traffic_reset_time = client.traffic_reset_time || '00:00'
    if (node.traffic_reset_timezone !== client.traffic_reset_timezone)
      node.traffic_reset_timezone = client.traffic_reset_timezone || 'UTC'
    if (node.created_at !== client.created_at)
      node.created_at = client.created_at
    if (node.updated_at !== client.updated_at)
      node.updated_at = client.updated_at
    return weightChanged
  }

  /**
   * 初始化节点数据（首次加载）
   */
  function initNodes(clients: Record<string, Client>, statuses: Record<string, NodeStatus>): void {
    const newUuids = new Set(Object.keys(clients))

    // 更新现有节点或添加新节点（就地合并，保持对象引用稳定）
    for (const [uuid, client] of Object.entries(clients)) {
      if (!client)
        continue

      const status = statuses[uuid]
      const existing = nodeIndex.get(uuid)

      if (existing) {
        applyClient(existing, client)
        if (status)
          applyStatus(existing, status)
      }
      else {
        const node = createNodeFromClient(client)
        if (status)
          applyStatus(node, status)
        addIndexedNode(node)
      }
    }

    // 移除不存在的节点
    for (let i = nodes.value.length - 1; i >= 0; i--) {
      const node = nodes.value[i]
      if (node && !newUuids.has(node.uuid)) {
        nodeIndex.delete(node.uuid)
        nodes.value.splice(i, 1)
      }
    }

    // 按 weight 升序排序（weight 越小越靠前）
    sortNodesByWeight()
  }

  /**
   * 按 weight 升序排序节点（weight 越小越靠前）
   */
  function sortNodesByWeight(): void {
    nodes.value.sort((a, b) => a.weight - b.weight)
  }

  /**
   * 更新节点状态（实时更新）
   */
  function updateNodeStatuses(statuses: Record<string, NodeStatus>): void {
    for (const [uuid, status] of Object.entries(statuses)) {
      const node = nodeIndex.get(uuid)
      if (node && status)
        applyStatus(node, status)
    }
  }

  /**
   * 更新节点基本信息
   */
  function updateNodeClients(clients: Record<string, Client>): void {
    const newUuids = new Set(Object.keys(clients))
    // 仅在结构或 weight 变化时才重排序，避免每轮轮询都触发数组排序的响应式开销
    let needSort = false

    // 更新现有节点信息或添加新节点（就地合并，保留已有状态信息）
    for (const [uuid, client] of Object.entries(clients)) {
      if (!client)
        continue

      const node = nodeIndex.get(uuid)
      if (node) {
        if (applyClient(node, client))
          needSort = true
      }
      else {
        // 添加新节点（不带状态）
        const newNode = createNodeFromClient(client)
        addIndexedNode(newNode)
        needSort = true
      }
    }

    // 移除不存在的节点
    for (let i = nodes.value.length - 1; i >= 0; i--) {
      const node = nodes.value[i]
      if (node && !newUuids.has(node.uuid)) {
        nodeIndex.delete(node.uuid)
        nodes.value.splice(i, 1)
        needSort = true
      }
    }

    if (needSort) {
      // 按 weight 升序排序
      sortNodesByWeight()
    }
  }

  /**
   * 更新 WebSocket 连接状态
   */
  function updateWsState(state: WsConnectionState, attempts?: number): void {
    wsConnectionState.value = state
    if (attempts !== undefined) {
      wsReconnectAttempts.value = attempts
    }
  }

  function applyTrafficCycleUsage(usage: ReadonlyMap<string, { up: number, down: number, start: string, nextReset: string }>): void {
    for (const node of nodes.value) {
      const value = usage.get(node.uuid)
      if (!value) {
        node.traffic_cycle_up = undefined
        node.traffic_cycle_down = undefined
        node.traffic_cycle_start = undefined
        node.traffic_cycle_next_reset = undefined
        node.traffic_cycle_ready = false
        continue
      }
      node.traffic_cycle_up = Math.max(0, value.up)
      node.traffic_cycle_down = Math.max(0, value.down)
      node.traffic_cycle_start = value.start
      node.traffic_cycle_next_reset = value.nextReset
      node.traffic_cycle_ready = true
    }
  }

  function clearTrafficCycleUsage(): void {
    for (const node of nodes.value) {
      node.traffic_cycle_up = undefined
      node.traffic_cycle_down = undefined
      node.traffic_cycle_start = undefined
      node.traffic_cycle_next_reset = undefined
      node.traffic_cycle_ready = false
    }
  }

  /**
   * 清空所有节点数据
   */
  function clearNodes(): void {
    nodes.value = []
    nodeIndex.clear()
  }

  return {
    // 状态
    nodes,
    wsConnectionState,
    wsReconnectAttempts,
    // 计算属性
    visibleNodes,
    onlineCount,
    totalCount,
    groups,
    nodesByUuid,
    visibleNodesByUuid,
    // 方法
    initNodes,
    updateNodeStatuses,
    updateNodeClients,
    sortNodesByWeight,
    updateWsState,
    applyTrafficCycleUsage,
    clearTrafficCycleUsage,
    clearNodes,
  }
})

export { useNodesStore }
