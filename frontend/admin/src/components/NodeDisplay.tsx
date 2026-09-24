import React, { useState, useMemo, useRef, useEffect, Suspense } from "react";
import {
  Flex,
  Text,
  IconButton,
  TextField,
  SegmentedControl,
} from "@radix-ui/themes";
import { Search, Grid3X3, Table2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLocalStorage } from "@/hooks/useLocalStorage";
import type { NodeBasicInfo } from "@/contexts/NodeListContext";
import type { LiveData } from "../types/LiveData";
import { NodeGrid } from "./Node";
const NodeTable = React.lazy(() => import("./NodeTable"));
import { isRegionMatch } from "@/utils/regionHelper";
import "./NodeDisplay.css";

export type ViewMode = "grid" | "table";

interface NodeDisplayProps {
  nodes: NodeBasicInfo[];
  liveData: LiveData;
}

const NodeDisplay: React.FC<NodeDisplayProps> = ({ nodes, liveData }) => {
  const [t] = useTranslation();
  const [viewMode, setViewMode] = useLocalStorage<ViewMode>(
    "nodeViewMode",
    "grid",
  );
  const [searchTerm, setSearchTerm] = useState("");
  const [selectedGroup, setSelectedGroup] = useLocalStorage<string>(
    "nodeSelectedGroup",
    "all",
  );
  const searchRef = useRef<HTMLInputElement>(null);
  const onlineSet = useMemo(() => new Set(liveData?.online ?? []), [liveData]);

  // 获取所有的分组
  const groups = useMemo(() => {
    const groupSet = new Set<string>();
    nodes.forEach((node) => {
      if (node.group && node.group.trim()) {
        groupSet.add(node.group);
      }
    });
    return Array.from(groupSet).sort();
  }, [nodes]);

  // 判断是否显示分组选择器
  const showGroupSelector = groups.length >= 1;

  // 键盘快捷键支持
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // 按 "/" 键聚焦搜索框
      if (e.key === "/" && !e.ctrlKey && !e.metaKey && !e.altKey) {
        e.preventDefault();
        searchRef.current?.focus();
      }
      // 按 Escape 键清空搜索
      if (e.key === "Escape" && searchTerm) {
        setSearchTerm("");
        searchRef.current?.blur();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [searchTerm]);

  // 过滤节点
  const filteredNodes = useMemo(() => {
    let result = nodes;

    // 先按分组过滤
    if (selectedGroup !== "all") {
      result = result.filter((node) => node.group === selectedGroup);
    }

    // 再按搜索条件过滤
    if (!searchTerm.trim()) return result;

    const term = searchTerm.toLowerCase().trim();
    return result.filter((node) => {
      // 基本信息搜索
      const basicMatch =
        node.name.toLowerCase().includes(term) ||
        node.os.toLowerCase().includes(term) ||
        node.arch.toLowerCase().includes(term);

      // 地区搜索（支持emoji和地区名称）
      const regionMatch = isRegionMatch(node.region, term);

      // 价格搜索（如果输入数字）
      const priceMatch =
        !isNaN(Number(term)) && node.price.toString().includes(term);

      // 状态搜索
      const isOnline = onlineSet.has(node.uuid);
      const statusMatch =
        ((term === "online" || term === "在线") && isOnline) ||
        ((term === "offline" || term === "离线") && !isOnline);

      return basicMatch || regionMatch || priceMatch || statusMatch;
    });
  }, [nodes, searchTerm, onlineSet, selectedGroup]);

  return (
    <div className="km-node-display w-full">
      {/* 控制栏 */}
      <Flex
        direction={{ initial: "column", sm: "row" }}
        justify="between"
        align={{ initial: "stretch", sm: "center" }}
        gap="4"
        className="control-bar mb-2 p-4 rounded-lg"
      >
        {/* 搜索框 */}
        <Flex
          align="center"
          gap="2"
          className="flex-1 max-w-md relative"
          wrap="wrap"
        >
          <TextField.Root
            ref={searchRef}
            placeholder={t("search.placeholder")}
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            className="search-box flex-1 pr-8 min-w-32"
          >
            <TextField.Slot>
              <Search size={16} />
            </TextField.Slot>
          </TextField.Root>
          {searchTerm && (
            <IconButton
              variant="ghost"
              size="1"
              className="absolute right-2 h-6 w-6 search-clear-button"
              onClick={() => {
                setSearchTerm("");
                searchRef.current?.focus();
              }}
              title={t("common.clear_search", "Clear search")}
              aria-label={t("common.clear_search", "Clear search")}
            >
              <X size={12} />
            </IconButton>
          )}
        </Flex>

        {/* 视图模式切换 */}
        <Flex align="center" gap="2">
          <label className="whitespace-nowrap text-md text-muted-foreground">
            {t("view.mode")}
          </label>
          <Flex gap="1">
            <IconButton
              variant={viewMode === "grid" ? "solid" : "soft"}
              onClick={() => setViewMode("grid")}
              className="transition-colors view-switch-button"
            >
              <Grid3X3 size={16} />
            </IconButton>
            <IconButton
              variant={viewMode === "table" ? "solid" : "soft"}
              onClick={() => setViewMode("table")}
              className="transition-colors view-switch-button"
            >
              <Table2 size={16} />
            </IconButton>
          </Flex>
        </Flex>
      </Flex>
      {/* 分组选择器 */}
      {showGroupSelector && (
        <Flex
          align="center"
          gap="2"
          className="km-node-group mx-4 mb-2 -mt-2 overflow-x-auto"
        >
          <label className="whitespace-nowrap text-md text-muted-foreground">
            {t("common.group")}
          </label>
          <SegmentedControl.Root
            value={selectedGroup}
            onValueChange={setSelectedGroup}
            size="1"
          >
            <SegmentedControl.Item value="all">
              {t("common.all")}
            </SegmentedControl.Item>
            {groups.map((group) => (
              <SegmentedControl.Item key={group} value={group} className="km-node-group-name">
                {group}
              </SegmentedControl.Item>
            ))}
          </SegmentedControl.Root>
        </Flex>
      )}
      {/* 搜索结果统计 */}
      <Flex justify="between" align="center" className="km-node-summary mx-4 mb-2">
        {searchTerm.trim() ? (
          <Text size="2" color="gray">
            {t("search.results", {
              count: filteredNodes.length,
              total:
                selectedGroup === "all"
                  ? nodes.length
                  : nodes.filter((n) => n.group === selectedGroup).length,
            })}
          </Text>
        ) : (
          <Text size="2" color="gray">
            {selectedGroup === "all"
              ? t("nodeCard.totalNodes", {
                  total: nodes.length,
                  online: liveData?.online?.length || 0,
                })
              : t("nodeCard.groupNodes", {
                  group: selectedGroup,
                  total: filteredNodes.length,
                  online: filteredNodes.filter((n) =>
                    onlineSet.has(n.uuid),
                  ).length,
                })}
          </Text>
        )}
      </Flex>

      {/* 节点显示区域 */}
      {filteredNodes.length === 0 ? (
        <Flex
          direction="column"
          align="center"
          justify="center"
          className="py-16 mx-4"
        >
          <Text size="4" color="gray" className="mb-2">
            {searchTerm.trim()
              ? t("search.no_results")
              : t("nodes.empty")}
          </Text>
          {searchTerm.trim() && (
            <Text size="2" color="gray">
              {t("search.try_different")}
            </Text>
          )}
        </Flex>
      ) : (
        <>
          {viewMode === "grid" ? (
            <NodeGrid
              nodes={filteredNodes}
              liveData={liveData}
              onlineSet={onlineSet}
            />
          ) : (
            <Suspense
              fallback={<div style={{ padding: 16 }}>Loading table…</div>}
            >
              <NodeTable
                nodes={filteredNodes}
                liveData={liveData}
                onlineSet={onlineSet}
              />
            </Suspense>
          )}
        </>
      )}
    </div>
  );
};

export default NodeDisplay;
