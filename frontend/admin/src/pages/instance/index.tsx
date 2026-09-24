import { useCallback, useEffect, useState, useMemo } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useLiveData } from "../../contexts/LiveDataContext";
import { useTranslation } from "react-i18next";
import type { Record } from "../../types/LiveData";
import Flag from "../../components/Flag";
import { Card, Flex, Text } from "@radix-ui/themes";
import { useNodeList } from "@/contexts/NodeListContext";
import { liveDataToRecords } from "@/utils/RecordHelper";
import LoadChart from "./LoadChart";
import { DetailsGrid } from "@/components/DetailsGrid";
import { usePublicInfo } from "@/contexts/PublicInfoContext";
import { useIsMobile } from "@/hooks/use-mobile";
import { AccountProvider } from "@/contexts/AccountContext";

export default function InstancePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { onRefresh, live_data } = useLiveData();
  const { uuid } = useParams<{ uuid: string }>();
  const [recent, setRecent] = useState<Record[]>([]);
  const [chartRealtimeActive, setChartRealtimeActive] = useState(true);
  const { nodeList } = useNodeList();
  const length = 30 * 5;
  // #region 初始数据加载
  const node = nodeList?.find((n) => n.uuid === uuid);
  const { publicInfo } = usePublicInfo();
  const isMobile = useIsMobile();
  const showServerListInDetails =
    publicInfo?.theme_settings?.showServerListInDetails === true;
  const offlineServerPosition =
    publicInfo?.theme_settings?.offlineServerPosition;
  const onlineSet = useMemo(
    () => new Set(live_data?.data?.online ?? []),
    [live_data?.data?.online],
  );
  const chartRecords = useMemo(
    () => liveDataToRecords(uuid ?? "", recent),
    [uuid, recent],
  );
  const handleChartRealtimeChange = useCallback((active: boolean) => {
    setChartRealtimeActive(active);
  }, []);

  // 组织按分组的服务器列表
  const groupedNodes = useMemo(() => {
    if (!nodeList) return [];

    const sortNodes = (
      a: (typeof nodeList)[number],
      b: (typeof nodeList)[number],
    ) => {
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
    };

    const groups = new Map<string | null, typeof nodeList>();

    nodeList.forEach((node) => {
      const groupKey = node.group && node.group.trim() ? node.group : null;
      if (!groups.has(groupKey)) {
        groups.set(groupKey, []);
      }
      groups.get(groupKey)?.push(node);
    });

    // 转换为数组，其中未分组的排在最后
    const result: Array<{ group: string | null; nodes: typeof nodeList }> = [];

    // 先添加有分组的（按分组名称排序）
    Array.from(groups.entries())
      .filter(([group]) => group !== null)
      .sort(([a], [b]) => (a ?? "").localeCompare(b ?? ""))
      .forEach(([group, nodes]) => {
        result.push({
          group,
          nodes: [...nodes].sort(sortNodes),
        });
      });

    // 再添加未分组的
    const ungrouped = groups.get(null);
    if (ungrouped) {
      result.push({
        group: null,
        nodes: [...ungrouped].sort(sortNodes),
      });
    }

    return result;
  }, [nodeList, onlineSet, offlineServerPosition]);

  useEffect(() => {
    if (!uuid) {
      setRecent([]);
      return;
    }

    const controller = new AbortController();
    setRecent([]);

    fetch(`/api/recent/${uuid}`, { signal: controller.signal })
      .then((res) => res.json())
      .then((data) => {
        if (!controller.signal.aborted) {
          setRecent((data?.data ?? []).slice(-length));
        }
      })
      .catch((err) => {
        if (err?.name !== "AbortError") {
          console.error("Failed to fetch recent data:", err);
        }
      });

    return () => controller.abort();
  }, [uuid, length]);
  // 动态追加数据
  useEffect(() => {
    const unsubscribe = onRefresh((resp) => {
      if (!uuid || !chartRealtimeActive) return;
      const data = resp.data.data[uuid];
      if (!data) return;

      setRecent((prev) => {
        const newRecord: Record = data;
        // 追加新数据，限制总长度为length（FIFO）
        // 检查是否已存在相同时间戳的记录
        const exists = prev.some(
          (item) => item.updated_at === newRecord.updated_at,
        );
        if (exists) {
          return prev; // 如果已存在，不添加新记录
        }

        // 否则，追加新记录
        const updated = [...prev, newRecord].slice(-length);
        return updated;
      });
    });

    // 清理订阅
    return unsubscribe;
  }, [chartRealtimeActive, length, onRefresh, uuid]);
  // #region 布局
  return (
    <div className="km-page-instance flex flex-row justify-center p-4 gap-4">
      {showServerListInDetails && !isMobile && (
        <div className="km-instance-server-list w-[300px] shrink-0 self-start sticky top-4">
          <Card
            className="km-instance-server-list-card w-full overflow-hidden shadow-lg"
            style={{ height: "calc(100vh - 2rem)" }}
          >
            <Flex direction="column" gap="0" className="h-full min-h-0">
              <div className="km-instance-server-list-header p-3 border-b border-accent-3">
                <Text size="2" weight="bold">
                  {t("common.serverList")}
                </Text>
              </div>
              <div className="km-instance-server-list-items flex-1 min-h-0 overflow-y-auto overscroll-contain">
                {groupedNodes.map((group, groupIndex) => (
                  <div key={groupIndex}>
                    {group.group && (
                      <div className="km-instance-server-group px-3 py-1 text-xs font-semibold text-accent-8 bg-accent-2 sticky top-0">
                        {group.group}
                      </div>
                    )}
                    {group.group === null && (
                      <div className="km-instance-server-group px-3 py-1 text-xs font-semibold text-accent-8 bg-accent-2 sticky top-0">
                        {t("common.ungrouped")}
                      </div>
                    )}
                    <div>
                      {group.nodes.map((node) => (
                        <div
                          key={node.uuid}
                          role="link"
                          tabIndex={0}
                          onClick={() => navigate(`/instance/${node.uuid}`)}
                          className={`km-instance-server-item mx-1 my-0.5 px-2 py-0 cursor-pointer transition-colors text-sm rounded-md border-l-[4px] flex items-center gap-2 ${
                            node.uuid === uuid
                              ? "bg-accent-4 text-accent-10 font-bold"
                              : "hover:bg-accent-3"
                          }`}
                          style={{
                            borderLeft:
                              node.uuid === uuid
                                ? "4px solid var(--accent-8)"
                                : "4px solid transparent",
                          }}
                        >
                          <Flag flag={node.region} />
                          <span
                            className={`truncate ${
                              node.uuid === uuid ? "text-accent-10" : ""
                            }`}
                          >
                            {node.name}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </Flex>
          </Card>
        </div>
      )}
      <div className="km-instance-main flex flex-col h-full items-center gap-2">
        <div className="km-instance-header flex flex-col gap-1 md:p-4 p-3 border-0 rounded-md">
          <h1 className="km-instance-title flex items-center flex-wrap">
            <Flag flag={node?.region ?? ""} />
            <Text size="3" weight="bold" wrap="nowrap">
              {node?.name ?? uuid}
            </Text>
            <Text
              size="1"
              style={{
                marginLeft: "8px",
              }}
              className="text-accent-6"
              wrap="nowrap"
            >
              {node?.uuid}
            </Text>
          </h1>
          <DetailsGrid
            box
            align="center"
            uuid={uuid ?? ""}
            node={node}
            liveRecord={uuid ? live_data?.data.data[uuid] : undefined}
          />
        </div>
        <AccountProvider>
          <LoadChart
            data={chartRecords}
            onRealtimeActiveChange={handleChartRealtimeChange}
          />
        </AccountProvider>
      </div>
    </div>
  );
}
