import React from "react";
import {
  Card,
  Flex,
  Text,
  Badge,
  Separator,
  IconButton,
} from "@radix-ui/themes";
import type { LiveData, Record } from "../types/LiveData";
import UsageBar from "./UsageBar";
import Flag from "./Flag";
import { useTranslation } from "react-i18next";
import Tips from "./ui/tips";

import { formatBytes } from "@/utils/unitHelper";

/** 格式化秒*/
export function formatUptime(seconds: number, t: TFunction): string {
  if (!seconds || seconds < 0) return t("nodeCard.time_second", { val: 0 });
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  const parts = [];
  if (d) parts.push(`${d} ${t("nodeCard.time_day")}`);
  if (h) parts.push(`${h} ${t("nodeCard.time_hour")}`);
  if (m) parts.push(`${m} ${t("nodeCard.time_minute")}`);
  if (s || parts.length === 0) parts.push(`${s} ${t("nodeCard.time_second")}`);
  return parts.join(" ");
}

interface NodeProps {
  basic: NodeBasicInfo;
  live: Record | undefined;
  online: boolean;
  isMobile: boolean;
  showIpTagsInCard: boolean;
}
const DEFAULT_NODE_LIVE = {
  cpu: { usage: 0 },
  ram: { used: 0 },
  swap: { used: 0 },
  load: { load1: 0, load5: 0, load15: 0 },
  disk: { used: 0 },
  network: { up: 0, down: 0, totalUp: 0, totalDown: 0 },
  connections: { tcp: 0, udp: 0 },
  uptime: 0,
  process: 0,
  message: "",
  updated_at: "",
} as Record;

const Node = React.memo(
  ({ basic, live, online, isMobile, showIpTagsInCard }: NodeProps) => {
  const [t] = useTranslation();
  const liveData = live || DEFAULT_NODE_LIVE;
  const osImage = React.useMemo(() => getOSImage(basic.os), [basic.os]);
  const osName = React.useMemo(() => getOSName(basic.os), [basic.os]);

  const memoryUsagePercent = basic.mem_total
    ? (liveData.ram.used / basic.mem_total) * 100
    : 0;
  const diskUsagePercent = basic.disk_total
    ? (liveData.disk.used / basic.disk_total) * 100
    : 0;

  const uploadSpeed = formatBytes(liveData.network.up);
  const downloadSpeed = formatBytes(liveData.network.down);
  const totalUpload = formatBytes(liveData.network.totalUp);
  const totalDownload = formatBytes(liveData.network.totalDown);
  //const totalTraffic = formatBytes(liveData.network.totalUp + liveData.network.totalDown);
  return (
    <Card
      style={{
        width: "100%",
        margin: "0 auto",
        transition: "all 0.2s ease-in-out",
      }}
      id={basic.uuid}
      className="km-node-card node-card hover:cursor-pointer hover:shadow-lg hover:bg-accent-2"
    >
      <Flex direction="column" gap="2">
        <Flex justify="between" align="center" my={isMobile ? "-1" : "0"}>
          <Flex justify="start" align="center" style={{ flex: 1, minWidth: 0 }} className="km-node-region">
            <Flag flag={basic.region} />
            <Link
              to={`/instance/${basic.uuid}`}
              style={{ flex: 1, minWidth: 0 }}
            >
              <Flex direction="column" style={{ minWidth: 0 }}>
                <Text
                  className="km-node-name"
                  weight="bold"
                  size={isMobile ? "2" : "4"}
                  truncate
                  style={{ maxWidth: "100%" }}
                >
                  {basic.name}
                </Text>
                <Text
                  color="gray"
                  hidden={!isMobile}
                  style={{
                    marginTop: "-3px",
                    fontSize: "0.728rem",
                  }}
                  className="text-sm"
                >
                  {formatUptime(liveData.uptime, t)}
                </Text>
                <PriceTags
                  hidden={isMobile}
                  price={basic.price}
                  billing_cycle={basic.billing_cycle}
                  expired_at={basic.expired_at}
                  currency={basic.currency}
                  tags={basic.tags}
                  ip4={showIpTagsInCard ? basic.ipv4 : undefined}
                  ip6={showIpTagsInCard ? basic.ipv6 : undefined}
                />
              </Flex>
            </Link>
          </Flex>
          <Flex gap="2" align="center" style={{ flex: "none" }} className="km-node-chart">
            {live?.message && <Tips color="#CE282E">{live.message}</Tips>}
            <MiniPingChartFloat
              uuid={basic.uuid}
              hours={24}
              trigger={
                <IconButton
                  variant="ghost"
                  size="1"
                  title={t("nodeCard.chart", "Chart")}
                  aria-label={t("nodeCard.chart", "Chart")}
                >
                  <TrendingUp size="14" />
                </IconButton>
              }
            />
            <Badge color={online ? "green" : "red"} variant="soft" className="km-node-status">
              {online ? t("nodeCard.online") : t("nodeCard.offline")}
            </Badge>
          </Flex>
        </Flex>

        <Separator size="4" className="-mt-1" />

        <Flex direction="column" gap="2">
          <Flex justify="between" hidden={isMobile}>
            <Text size="2" color="gray">
              OS
            </Text>
            <Flex align="center">
              <img
                src={osImage}
                alt={basic.os}
                className="w-5 h-5 mr-2"
              />
              <Text size="2">
                {osName} / {basic.arch}
              </Text>
            </Flex>
          </Flex>
          <Flex className="md:flex-col flex-row md:gap-1 gap-4">
            {/* CPU Usage */}
            <UsageBar label={t("admin.nodeDetail.cpu")} value={liveData.cpu.usage} />

            {/* Memory Usage */}
            <UsageBar label={t("nodeCard.ram")} value={memoryUsagePercent} />
            <Text
              className="md:block hidden"
              size="1"
              color="gray"
              style={{ marginTop: "-4px" }}
            >
              ({formatBytes(liveData.ram.used)} / {formatBytes(basic.mem_total)}
              )
            </Text>

            {/* Disk Usage */}
            <UsageBar label={t("nodeCard.disk")} value={diskUsagePercent} />
            <Text
              size="1"
              className="md:block hidden"
              color="gray"
              style={{ marginTop: "-4px" }}
            >
              ({formatBytes(liveData.disk.used)} /{" "}
              {formatBytes(basic.disk_total)})
            </Text>
          </Flex>
          {basic.traffic_limit > 0 ? (
            <Flex justify="between" hidden={isMobile} direction="column">
              <UsageBar
                label={t("nodeCard.totalTraffic")}
                value={getTrafficPercentage(
                  liveData.network.totalUp,
                  liveData.network.totalDown,
                  basic.traffic_limit,
                  basic.traffic_limit_type ?? "sum",
                )}
                max={Infinity}
              />
              <Flex wrap="nowrap" justify="between">
                <Text size="1" className="md:block hidden" color="gray">
                  ↑ {totalUpload} ↓ {totalDownload}
                </Text>
                <Text size="1" className="md:block hidden" color="gray">
                  {basic.traffic_limit_type &&
                    basic.traffic_limit_type.charAt(0).toUpperCase() +
                      basic.traffic_limit_type.slice(1)}
                  ({formatBytes(basic.traffic_limit)})
                </Text>
              </Flex>
            </Flex>
          ) : (
            <Flex justify="between" hidden={isMobile}>
              <Text size="2" color="gray">
                {t("nodeCard.totalTraffic")}
              </Text>
              <Text size="2">
                ↑ {totalUpload} ↓ {totalDownload}
              </Text>
            </Flex>
          )}

          <Flex justify="between" hidden={isMobile}>
            <Text size="2" color="gray" className="flex items-center">
              {t("nodeCard.networkSpeed")}
            </Text>
            <Text size="2">
              ↑ {uploadSpeed}/s ↓ {downloadSpeed}/s
            </Text>
          </Flex>

          <Flex justify="between" gap="2" hidden={!isMobile}>
            <Text size="2">{t("nodeCard.networkSpeed")}</Text>
            <Text size="2">
              ↑ {uploadSpeed}/s ↓ {downloadSpeed}/s
            </Text>
          </Flex>
          <Flex justify="between" gap="2" hidden={!isMobile}>
            <Text size="2">{t("nodeCard.totalTraffic")}</Text>
            <Flex direction="column">
              <Text size="2">
                ↑ {totalUpload} ↓ {totalDownload}
              </Text>
            </Flex>
          </Flex>
          {basic.traffic_limit > 0 && isMobile && (
            <UsageBar
              label={`${basic.traffic_limit_type && basic.traffic_limit_type.charAt(0).toUpperCase() + basic.traffic_limit_type.slice(1)}(${formatBytes(basic.traffic_limit)})`}
              max={Infinity}
              value={getTrafficPercentage(
                liveData.network.totalUp,
                liveData.network.totalDown,
                basic.traffic_limit,
                basic.traffic_limit_type ?? "sum",
              )}
            />
          )}
          <Flex justify="between" hidden={isMobile}>
            <Text size="2" color="gray">
              {t("nodeCard.uptime")}
            </Text>
            {online ? (
              <Text size="2">{formatUptime(liveData.uptime, t)}</Text>
            ) : (
              <Text size="2" color="gray">
                -
              </Text>
            )}
          </Flex>
        </Flex>
        <PriceTags
          hidden={!isMobile}
          price={basic.price}
          billing_cycle={basic.billing_cycle}
          expired_at={basic.expired_at}
          currency={basic.currency}
          tags={basic.tags || ""}
        />
      </Flex>
    </Card>
  );
});

export default Node;

type NodeGridProps = {
  nodes: NodeBasicInfo[];
  liveData: LiveData;
  onlineSet: ReadonlySet<string>;
};

import { Box } from "@radix-ui/themes";
import type { TFunction } from "i18next";
import { Link } from "react-router-dom";
import { useIsMobile } from "@/hooks/use-mobile";
import type { NodeBasicInfo } from "@/contexts/NodeListContext";
import PriceTags from "./PriceTags";
import { TrendingUp } from "lucide-react";
import MiniPingChartFloat from "./MiniPingChartFloat";
import { getOSImage, getOSName } from "@/utils";
import { usePublicInfo } from "@/contexts/PublicInfoContext";
export const NodeGrid = ({ nodes, liveData, onlineSet }: NodeGridProps) => {
  const { publicInfo } = usePublicInfo();
  const isMobile = useIsMobile();
  const showIpTagsInCard = Boolean(
    publicInfo?.theme_settings?.showIpTagsInCard,
  );
  const offlineServerPosition =
    publicInfo?.theme_settings?.offlineServerPosition; // "First/Keep/Last"

  // 排序节点：先按权重排序，权重大的靠前，再根据用户设置排序
  const sortedNodes = React.useMemo(() => [...nodes].sort((a, b) => {
    const aIsOnline = onlineSet.has(a.uuid);
    const bIsOnline = onlineSet.has(b.uuid);

    if (offlineServerPosition === "First") {
      if (!aIsOnline && bIsOnline) return -1;
      if (aIsOnline && !bIsOnline) return 1;
    } else if (offlineServerPosition !== "Keep") {
      if (aIsOnline && !bIsOnline) return -1;
      if (!aIsOnline && bIsOnline) return 1;
    }
    return a.weight - b.weight;
  }), [nodes, offlineServerPosition, onlineSet]);

  return (
    <Box
      className="km-node-list gap-2 md:gap-4"
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))",
        padding: "1rem",
        width: "100%",
        boxSizing: "border-box",
      }}
    >
      {sortedNodes.map((node) => {
        const isOnline = onlineSet.has(node.uuid);
        const nodeData = liveData.data[node.uuid];

        return (
          <Node
            key={node.uuid}
            basic={node}
            live={nodeData}
            online={isOnline}
            isMobile={isMobile}
            showIpTagsInCard={showIpTagsInCard}
          />
        );
      })}
    </Box>
  );
};

function getTrafficPercentage(
  totalUp: number,
  totalDown: number,
  limit: number,
  type: "max" | "min" | "sum" | "up" | "down",
) {
  if (limit === 0) return 0;
  switch (type) {
    case "max":
      return (Math.max(totalUp, totalDown) / limit) * 100;
    case "min":
      return (Math.min(totalUp, totalDown) / limit) * 100;
    case "sum":
      return ((totalUp + totalDown) / limit) * 100;
    case "up":
      return (totalUp / limit) * 100;
    case "down":
      return (totalDown / limit) * 100;
    default:
      return 0;
  }
}
