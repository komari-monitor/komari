import {
  Callout,
  Card,
  Flex,
  Text,
  Popover,
  IconButton,
  Switch,
} from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React, { useCallback, useEffect, useMemo, Suspense } from "react";
const NodeDisplay = React.lazy(() => import("../components/NodeDisplay"));
import { formatBytes } from "@/utils/unitHelper";
import { useLiveData } from "../contexts/LiveDataContext";
import { useNodeList } from "@/contexts/NodeListContext";
import Loading from "@/components/loading";
import { Settings } from "lucide-react";
import { useLocalStorage } from "@/hooks/useLocalStorage";
import type { LiveData } from "@/types/LiveData";

// Intelligent speed formatting function
const formatSpeed = (bytes: number): string => {
  if (bytes === 0) return "0 B/s";
  const units = ["B/s", "KB/s", "MB/s", "GB/s", "TB/s"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const size = bytes / Math.pow(1024, i);

  // Adaptive decimal places
  let decimals = 2;
  if (i >= 3) decimals = 1; // GB and above: 1 decimal
  if (i <= 1) decimals = 0; // B and KB: no decimals
  if (size >= 100) decimals = 0; // 100+ of any unit: no decimals

  return `${size.toFixed(decimals)} ${units[i]}`;
};

const EMPTY_LIVE_DATA: LiveData = { online: [], data: {} };

const STATUS_CARD_VISIBILITY_DEFAULTS = {
  currentTime: true,
  currentOnline: true,
  regionOverview: true,
  trafficOverview: true,
  networkSpeed: true,
};

type StatusCardKey = keyof typeof STATUS_CARD_VISIBILITY_DEFAULTS;

const Index = () => {
  const [t] = useTranslation();
  const { live_data } = useLiveData();
  const { nodeList, isLoading, error, refresh } = useNodeList();
  const liveData = live_data?.data ?? EMPTY_LIVE_DATA;
  const onlineSet = useMemo(
    () => new Set(liveData.online),
    [liveData.online],
  );
  const [statusCardsVisibility, setStatusCardsVisibility] = useLocalStorage<
    Record<StatusCardKey, boolean>
  >("statusCardsVisibility", STATUS_CARD_VISIBILITY_DEFAULTS);

  const summaryStats = useMemo(() => {
    const regions = new Set<string>();
    let totalUp = 0;
    let totalDown = 0;
    let speedUp = 0;
    let speedDown = 0;

    for (const node of nodeList ?? []) {
      if (!onlineSet.has(node.uuid)) continue;

      regions.add(node.region);
      const record = liveData.data[node.uuid];
      if (!record) continue;

      totalUp += record.network.totalUp || 0;
      totalDown += record.network.totalDown || 0;
      speedUp += record.network.up || 0;
      speedDown += record.network.down || 0;
    }

    return {
      regionCount: regions.size,
      trafficText: `↑ ${formatBytes(totalUp)} / ↓ ${formatBytes(totalDown)}`,
      speedText: `↑ ${formatSpeed(speedUp)} / ↓ ${formatSpeed(speedDown)}`,
    };
  }, [liveData.data, nodeList, onlineSet]);

  const statusCards = useMemo(
    () => [
      {
        key: "currentTime" as const,
        title: t("current_time"),
        value: <CurrentTimeValue />,
        visible: statusCardsVisibility.currentTime,
      },
      {
        key: "currentOnline" as const,
        title: t("current_online"),
        value: `${onlineSet.size} / ${nodeList?.length ?? 0}`,
        visible: statusCardsVisibility.currentOnline,
      },
      {
        key: "regionOverview" as const,
        title: t("region_overview"),
        value: summaryStats.regionCount,
        visible: statusCardsVisibility.regionOverview,
      },
      {
        key: "trafficOverview" as const,
        title: t("traffic_overview"),
        value: summaryStats.trafficText,
        visible: statusCardsVisibility.trafficOverview,
      },
      {
        key: "networkSpeed" as const,
        title: t("network_speed"),
        value: summaryStats.speedText,
        visible: statusCardsVisibility.networkSpeed,
      },
    ],
    [nodeList?.length, onlineSet.size, statusCardsVisibility, summaryStats, t],
  );

  const visibleStatusCards = useMemo(
    () => statusCards.filter((card) => card.visible),
    [statusCards],
  );

  const updateStatusCardVisibility = useCallback(
    (key: StatusCardKey, checked: boolean) => {
      setStatusCardsVisibility((current) => ({
        ...current,
        [key]: checked,
      }));
    },
    [setStatusCardsVisibility],
  );

  useEffect(() => {
    let interval: number | undefined;
    const stopPolling = () => {
      if (interval !== undefined) {
        window.clearInterval(interval);
        interval = undefined;
      }
    };
    const startPolling = () => {
      if (interval === undefined && !document.hidden) {
        interval = window.setInterval(refresh, 5000);
      }
    };
    const handleVisibilityChange = () => {
      stopPolling();
      if (!document.hidden) {
        refresh();
        startPolling();
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    startPolling();
    return () => {
      stopPolling();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [refresh]);

  if (isLoading) {
    return <Loading />;
  }
  if (error) {
    return <div>Error: {error}</div>;
  }

  return (
    <>
      <Callouts />
      <Card className="km-page-index km-summary-card summary-card mx-4 md:text-base text-sm relative">
        <div className="km-summary-card-settings absolute top-2 right-2">
          <Popover.Root>
            <Popover.Trigger>
              <IconButton
                variant="ghost"
                size="1"
                title={t("status_settings", "Status Settings")}
                aria-label={t("status_settings", "Status Settings")}
              >
                <Settings size={16} />
              </IconButton>
            </Popover.Trigger>
            <Popover.Content width="300px">
              <Flex direction="column" gap="3">
                <Text size="2" weight="bold">
                  {t("status_settings")}
                </Text>
                <Flex direction="column" gap="2">
                  {statusCards.map((card) => (
                    <StatusSettingSwitch
                      key={card.key}
                      label={card.title}
                      checked={card.visible}
                      onCheckedChange={(checked) =>
                        updateStatusCardVisibility(card.key, checked)
                      }
                    />
                  ))}
                </Flex>
              </Flex>
            </Popover.Content>
          </Popover.Root>
        </div>

        <div
          className="km-summary-card-grid grid gap-2"
          style={{
            gridTemplateColumns: `repeat(auto-fit, minmax(230px, 1fr))`,
            gridAutoRows: "min-content",
          }}
        >
          {visibleStatusCards.map((card) => (
            <TopCard key={card.key} title={card.title} value={card.value} />
          ))}
        </div>
      </Card>
      <Suspense fallback={<div style={{ padding: 16 }}>Loading…</div>}>
        <NodeDisplay nodes={nodeList ?? []} liveData={liveData} />
      </Suspense>
    </>
  );
};

//#region Callouts
const Callouts = () => {
  const [t] = useTranslation();
  const { showCallout } = useLiveData();
  const ishttps = window.location.protocol === "https:";
  return (
    <Flex direction="column" gap="2" className="km-callouts m-2">
      <Callout.Root
        m="2"
        hidden={ishttps}
        color="red"
        className="km-callout km-callout-https"
      >
        <Callout.Icon>
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            viewBox="0 0 24 24"
          >
            <path
              fill="currentColor"
              d="M10.03 3.659c.856-1.548 3.081-1.548 3.937 0l7.746 14.001c.83 1.5-.255 3.34-1.969 3.34H4.254c-1.715 0-2.8-1.84-1.97-3.34zM12.997 17A.999.999 0 1 0 11 17a.999.999 0 0 0 1.997 0m-.259-7.853a.75.75 0 0 0-1.493.103l.004 4.501l.007.102a.75.75 0 0 0 1.493-.103l-.004-4.502z"
            />
          </svg>
        </Callout.Icon>
        <Callout.Text>
          <Text size="2" weight="medium">
            {t("warn_https")}
          </Text>
        </Callout.Text>
      </Callout.Root>
      <Callout.Root
        m="2"
        hidden={showCallout}
        id="callout"
        color="tomato"
        className="km-callout km-callout-websocket"
      >
        <Callout.Icon>
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            height="24"
            viewBox="0 0 24 24"
          >
            <path
              fill="currentColor"
              d="M21.707 3.707a1 1 0 0 0-1.414-1.414L18.496 4.09a4.25 4.25 0 0 0-5.251.604l-1.068 1.069a1.75 1.75 0 0 0 0 2.474l3.585 3.586a1.75 1.75 0 0 0 2.475 0l1.068-1.068a4.25 4.25 0 0 0 .605-5.25zm-11 8a1 1 0 0 0-1.414-1.414l-1.47 1.47l-.293-.293a.75.75 0 0 0-1.06 0l-1.775 1.775a4.25 4.25 0 0 0-.605 5.25l-1.797 1.798a1 1 0 1 0 1.414 1.414l1.798-1.797a4.25 4.25 0 0 0 5.25-.605l1.775-1.775a.75.75 0 0 0 0-1.06l-.293-.293l1.47-1.47a1 1 0 0 0-1.414-1.414l-1.47 1.47l-1.586-1.586z"
            />
          </svg>
        </Callout.Icon>
        <Callout.Text>
          <Text size="2" weight="medium">
            {t("warn_websocket")}
          </Text>
        </Callout.Text>
      </Callout.Root>
    </Flex>
  );
};
// #endregion Callouts
export default Index;

type TopCardProps = {
  title: string;
  value: React.ReactNode;
  description?: string;
};

const TopCard: React.FC<TopCardProps> = React.memo(
  ({ title, value, description }) => {
    return (
      <div className="km-top-card min-w-52 md:max-w-72 w-full">
        <Flex direction="column" gap="1">
          <label className="text-muted-foreground text-sm">{title}</label>
          <label className="font-medium -mt-2 text-md">{value}</label>
          {description && (
            <Text size="2" color="gray">
              {description}
            </Text>
          )}
        </Flex>
      </div>
    );
  },
);

const CurrentTimeValue = React.memo(() => {
  const [currentTime, setCurrentTime] = React.useState(() =>
    new Date().toLocaleTimeString(),
  );

  React.useEffect(() => {
    const timer = window.setInterval(() => {
      setCurrentTime(new Date().toLocaleTimeString());
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  return <>{currentTime}</>;
});

type StatusSettingSwitchProps = {
  label: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
};

const StatusSettingSwitch: React.FC<StatusSettingSwitchProps> = React.memo(
  ({ label, checked, onCheckedChange }) => {
    return (
      <Flex justify="between" align="center">
        <Text size="2">{label}</Text>
        <Switch checked={checked} onCheckedChange={onCheckedChange} />
      </Flex>
    );
  },
);
