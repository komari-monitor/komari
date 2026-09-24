# Komari 1.2.6 主题适配方案

## 基线

- 调研日期：2026-07-13。
- 前端：[komari-monitor/komari-web](https://github.com/komari-monitor/komari-web) `radix` 分支提交 [`ebfbd3e`](https://github.com/komari-monitor/komari-web/commit/ebfbd3e079f8777a746276fe67429b519024f7c7)。
- 后端与探针协议：[Komari 1.2.6](https://github.com/komari-monitor/komari/releases/tag/1.2.6)，提交 [`c828653`](https://github.com/komari-monitor/komari/commit/c828653d200786e165f9e678533a925e0cc60325)。
- 本主题仍只提供 `/` 与 `/instance/:id` 两个公开路由；Komari 官方后台继续负责管理和敏感操作。

  1.2.6 将 `records`、`gpu_records`、`ping_records` 迁移到 Metric Store，并支持 `PreserveSeries` 保留 GPU 设备、Ping 任务等标签维度。主题应优先消费 `public:queryMetrics`、`public:getPingMetricStats`，旧后端才回落到历史 records 接口。本仓库的 `metrics.service.ts`、`PingChart.vue` 和 `LoadChart.vue` 已按该策略实现。

## 页面价值矩阵

| 页面/区域     | 官方 1.2.6 能力                                                          | 本主题现状                                                 | 自定义建议                                            |
| ------------- | ------------------------------------------------------------------------ | ---------------------------------------------------------- | ----------------------------------------------------- |
| 首页状态区    | 时间、在线数、地区数、总流量、实时网速，可在访客本机开关                 | 已扩展为容量、运维、流量、财务、资产分布等总览卡           | 由站点管理员设置预设或英文逗号 keys                   |
| 首页节点卡    | CPU、内存、硬盘、流量、网速、运行时间、价格/IP 标签、消息、24h Ping 浮层 | 已有四档尺寸、流量预警、价格、消息、Ping 摘要和列表视图    | 下一阶段增加节点卡字段模板；IP 只在后端允许公开时展示 |
| 首页列表/筛选 | 搜索、分组、卡片/表格、离线节点位置                                      | 已有搜索、分组、卡片/列表、快捷排序与离线置底              | 可继续补“离线优先/保持权重/置底”三态和默认分组        |
| 节点详情概览  | 节点切换器、硬件/系统/网络信息                                           | 已有 18 个可选实时/财务指标、CPU 评分、硬件、系统、网络    | 使用 7 套预设或英文逗号 keys；旧价格四卡仍是默认值    |
| 节点负载      | 可组合 25 个 Metric Store definition、尺寸、拖拽、采样和平滑             | 25 个官方指标归并为 12 个图表族，保留 GPU/Ping 标签维度    | 预设/英文 keys 优先，保留旧卡位和 JSON 兼容           |
| 节点延迟      | 多任务延迟、min/max/avg、丢包、P50/P99、波动、EWMA                       | 已接入统计、metric series 和自定义起止时间，保留旧接口回落 | 可增加默认平滑和任务默认可见性设置                    |
| 高级工具      | 拓扑、健康摘要、性价比、快照、审计日志                                   | 登录并验证权限后按需加载                                   | 保持私有；不能仅靠隐藏按钮代替权限检查                |
| Komari 后台   | Metric Store、数据库维护、通知、Agent、命令、终端、2FA                   | 由官方后台提供                                             | 不复制进公开主题；顶部后台入口只负责跳转              |

## 本轮卡片适配

### 预设

- `官方`：当前时间、在线节点、地区分布、累计流量、实时上行、实时下行。
- `基础`：内存、硬盘、剩余价值、累计流量、实时上行、实时下行。
- `运维`：在线、离线、高负载、流量预警、平均 CPU、平均负载。
- `资源`：平均 CPU、平均负载、内存、硬盘、交换内存、CPU 核心。
- `GPU`：GPU 节点、平均 GPU、GPU 峰值、平均 CPU、内存、实时流量峰值。
- `资产`：在线节点、地区、系统、虚拟化、CPU 核心、GPU 节点。
- `财务`、`流量`、`完整` 保留原有用途。

### 自定义 keys

主题后台使用多行 `generalCardKeys` 直接填写英文 key，支持逗号、分号、空格或换行分隔。重复和非法 key 自动过滤；旧版本保存的 8 个下拉卡位仍作为隐藏兼容输入读取。后台 help 列出全部 key 与中文含义。

新增卡片：

- `currentTime`：访客浏览器本地时间，不发请求。
- `avgGpu`：在线 GPU 节点的实时平均使用率。
- `gpuNodes`：具有 GPU 名称或 GPU 实时数据的节点数，提示中列出型号。
- `gpuPeakNode`：在线节点实时 GPU 使用率峰值和对应节点。

GPU 总览只使用 node store 的实时公开字段，不为每张卡请求历史数据。无 GPU 数据时显示 `-` 或 `0 / 总节点`，不伪造设备状态。

## `/instance` 详情页适配

### 概览指标卡

详情页提供 `财务`、`状态`、`资源`、`网络`、`GPU`、`综合`、`自定义` 7 套方案。概览卡按移动端 2 列、中屏 3 列、宽屏 4 列响应式排列；财务、状态、网络、GPU 各 8 张，资源 12 张，综合 16 张。自定义模式读取多行 `detailMetricCardKeys`；旧版本保存的 8 个卡位仍兼容。

可用指标共 18 个：

- 财务：节点价格、月均支出、剩余时间、剩余价值。
- 实时负载：CPU 使用率、GPU 使用率、内存使用率、交换内存使用率、硬盘使用率、1/5/15 分钟系统负载、系统温度。
- 运行状态：进程数、TCP+UDP 连接数、运行时间。
- 网络：实时上行、实时下行、累计流量、流量配额使用率。

GPU 名称为空且实时 GPU 为 0 时显示无数据；交换内存、硬盘和流量配额的分母无效时显示 `-` 或无限流量。金额类指标继续服从“未登录隐藏价格”。

### 历史负载图

负载图提供 `默认`、`精简`、`资源`、`网络`、`GPU`、`延迟`、`运维`、`完整`、`自定义` 9 套方案。官方 25 个 definition 归并为 12 个稳定图表族：

- `cpu`：`cpu.usage` + `load.average`。
- `memory`：`memory.used/total` + `swap.used/total`。
- `disk`：`disk.used/total`。
- `network`：`net.in.rate` + `net.out.rate`。
- `traffic`：`net.total.up/down` + `traffic.up/down`。
- `gpu`：`gpu.usage` + 按设备拆分的 `gpu.device.usage`。
- `gpuMemory`：按设备拆分的 `gpu.memory.used/total`。
- `temperature`：`temperature` + 按设备拆分的 `gpu.temperature`。
- `connections`、`process`：TCP/UDP 连接与进程数。
- `ping`、`pingLoss`：按 `task_id` 拆分的延迟与丢包；丢包原始 0/1 转为百分比。

所有卡片使用统一图标化标题和不同语义色。GPU 族仍需显式开启 `gpuChartEnabled`，且没有 definition 或有效数据时自动隐藏。

兼容顺序：

1. 新配置选择自定义时解析逗号、分号、空格或换行分隔的 12 个图表族 key。
2. 检测到旧版 7 个卡位时优先保留旧顺序；旧 JSON 模板也继续兼容。
3. 非法、重复或空配置自动去重并回落默认图表顺序。

官方主题还允许访客在本机动态增删任意 definition、拖拽和切换 S/M/L。本主题使用站点级预设和有序 keys 保持所有访客布局一致；当前 12 个图表族已覆盖 Komari 1.2.6 固定创建的全部 25 个 definition，未来后端新增未知 definition 时再按单位与 metadata 扩展。

### Ping 丢包口径

- 新接口以 `public:getPingMetricStats` 的 `ping.loss` 统计为准；`public:queryMetrics` 中的 `null` 是填充空桶，不是丢包。
- 旧 records 的负值是 Komari 明确使用的丢包哨兵，1.2.6 后端写入 Metric Store 时同样将 `value < 0` 转成 `ping.loss=1`，因此 fallback 应保留该判断。
- 独立 Ping 图的自定义时间向新接口传精确 `start` / `end`；新接口无有效时序点时回落 `common:getRecords`，按可用保留时长回溯后再裁剪到所选区间。
- 首页多任务汇总保留 100% 丢包任务，并按各任务总样本数加权；loss stats 缺失或仅为 approximate 时整体回落旧接口，避免把未知丢包显示成 0%。

## 主题设置重构

Komari 的 managed theme 表单只支持标题、开关、下拉、数字、短文本和长文本。本主题按使用路径重排为 8 个编号分区，并将原有逐项卡位压缩为多行英文 keys 字段。首页卡片、快捷按钮、列表字段、详情概览和详情图表的 help 均列出完整 key 含义；旧卡位和旧图表 JSON 只在代码中保留兼容。

## 旧 iOS 边界

- 正式视觉基线仍是 Tailwind CSS v4 官方要求的 Safari/iOS 16.4+。
- JavaScript/CSS 构建 target 下调到 Safari 15.4；iOS 15.4-16.3 缺少 `oklch`/`color-mix` 时使用 sRGB token，并关闭卡片 backdrop blur，保证中文和主要布局可读。
- 不支持 ESM 的更老内核显示升级提示，不加载一个样式仍无法兼容的重复 legacy 应用包。
- HTML 固定声明 UTF-8、`lang=zh-CN` 和 `viewport-fit=cover`。

## 后续优先级

### P1：设置可用性

1. 增加首页节点卡字段模板：运行时间、价格、IP 标签、Ping、消息、实时/累计流量。
2. 将离线节点位置从单一“置底”扩展为“优先 / 保持权重 / 置底”。
3. 增加详情页节点切换器开关，方便多节点巡检。
4. 为 Ping 增加默认时间范围、默认平滑和任务默认可见性设置。

### P2：Metric Store 派生视图

1. 健康摘要改为优先使用 Metric Store，统一 P50/P99、丢包和波动口径。
2. 增加需要登录的历史容量趋势和异常节点榜单，统一走 RequestManager 与 CacheService。
3. 后端新增 1.2.6 之外的自定义 definition 时，按 `unit`、`type`、`metadata` 增加安全的通用图表映射。

### 不纳入主题

- 数据库 Vacuum、Metric Store DSN/迁移、通知规则、Agent 管理、命令执行、Web 终端和 2FA。
- 这些功能具有写操作或敏感数据边界，应继续使用 Komari 官方后台的 Principal/2FA 权限校验。

## 验收

- 旧的 `generalCardPreset`、`generalCardKeys`、`chartDashboardTemplate` 配置升级后行为不变。
- 首页、详情概览、详情图表 keys 支持逗号、分号、空格和换行，顺序稳定，重复项去重，空配置回落各自默认值。
- 详情概览预设在宽屏每行 4 张，预设总数均为 4 的倍数；中屏和移动端分别降为 3 列和 2 列。
- GPU 卡片随 WebSocket/HTTP 实时数据更新，不刷新页面。
- GPU 设备和 Ping 任务按标签拆分序列；`null` 保持图表断点，`ping.loss` 以百分比显示。
- Ping 自定义范围在新接口使用精确起止时间，旧接口继续按 `value < 0` 识别丢包并在回溯后裁剪区间。
- 无 GPU、无价格、无流量配额、未登录四种场景均能降级展示。
- `bun run lint` 与 `bun run build` 通过，发布 zip 结构保持不变。
