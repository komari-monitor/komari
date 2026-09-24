import { useTranslation } from "react-i18next";
import { UpDownStack } from "./UpDownStack";
import {
  useNodeList,
  type NodeBasicInfo,
} from "@/contexts/NodeListContext";
import { useLiveData } from "@/contexts/LiveDataContext";
import { formatUptime } from "./Node";
import { formatBytes } from "@/utils/unitHelper";
import { Flex, Text, Card } from "@radix-ui/themes";
import type { Record as LiveRecord } from "@/types/LiveData";

type DetailsGridProps = {
  uuid: string;
  gap?: string;
  box?: boolean;
  align?: "start" | "center" | "end";
  node?: NodeBasicInfo;
  liveRecord?: LiveRecord;
};

export const DetailsGrid = ({
  uuid,
  gap,
  box,
  align,
  node: nodeProp,
  liveRecord,
}: DetailsGridProps) => {
  const { t } = useTranslation();

  const nodeListContext = useNodeList(false);
  const { live_data } = useLiveData();
  const node =
    nodeProp ?? nodeListContext?.nodeList?.find((n) => n.uuid === uuid);
  const currentRecord = liveRecord ?? live_data?.data.data[uuid ?? ""];

  const Container: any = box ? Card : 'div';

  return (
    <Container
      className={`km-details-grid DetailsGrid max-w-[900px]`}
    >
      <div className={`flex flex-wrap gap-${gap ?? "4"} basis-full justify-center ${align === "center" ? "justify-between" : ""}`}>
        <UpDownStack
          className="km-details-item md:w-128 flex-[0_0_calc(50%-0.5rem)]"
          up="CPU"
          down={`${node?.cpu_name} (x${node?.cpu_cores})`}
        />
        <label className={`km-details-item flex flex-wrap gap-2 gap-x-8 flex-[0_0_calc(50%-0.5rem)] ${align === "center" ? "justify-end" : ""}`}>
          <UpDownStack up={t("admin.nodeDetail.arch")} down={node?.arch ?? "Unknown"} />

          <UpDownStack
            up={t("nodeCard.virtualization")}
            align={align === "center" ? "end" : "start"}
            down={node?.virtualization ?? "Unknown"}
          />
        </label>
        <UpDownStack up="GPU" down={node?.gpu_name ?? "Unknown"} className="km-details-item flex-[0_0_calc(50%-0.5rem)]" />
        <div className={`km-details-item flex flex-col gap-0 flex-[0_0_calc(50%-0.5rem)] ${align === "center" ? "items-end text-right" : "items-start"}`}>
          <label className="text-base font-bold">{t("nodeCard.os")}</label>
          <label className="text-sm text-muted-foreground -mt-1">{node?.os ?? "Unknown"}</label>
          <label className="text-xs text-muted-foreground opacity-75">
            {t("nodeCard.kernelVersion")}: {node?.kernel_version ?? "Unknown"}
          </label>
        </div>

        <UpDownStack
          className="km-details-item md:w-64 w-full flex-[0_0_calc(50%-0.5rem)]"
          up={t("nodeCard.networkSpeed")}
          down={` ↑ ${formatBytes(
            currentRecord?.network.up || 0
          )}/s
          ↓
          ${formatBytes(
            currentRecord?.network.down || 0
          )}/s`}
        />
        <UpDownStack
          up={t("nodeCard.totalTraffic")}
          align={align === "center" ? "end" : "start"}
          className="km-details-item flex-[0_0_calc(50%-0.5rem)]"
            down={`↑
          ${formatBytes(
              currentRecord?.network.totalUp || 0
            )}
          ↓
          ${formatBytes(
              currentRecord?.network.totalDown || 0
            )}`}
        />
        <UpDownStack
          className="km-details-item md:w-70 w-full flex-[0_0_calc(50%-0.5rem)]"
          up={t("nodeCard.ram")}
          down={formatBytes(node?.mem_total || 0)}
        />
        <UpDownStack
          up={t("nodeCard.swap")}
          className="km-details-item flex-[0_0_calc(50%-0.5rem)]"
          align={align === "center" ? "end" : "start"}
          down={formatBytes(node?.swap_total || 0)}
        />
        <UpDownStack
          className="km-details-item md:w-64 w-full flex-[0_0_calc(50%-0.5rem)]"
          up={t("nodeCard.disk")}
          down={formatBytes(node?.disk_total || 0)}
        />
        <div className="flex-[0_0_calc(50%-0.5rem)]" />
        <UpDownStack
          up={t("nodeCard.uptime")}
          className="km-details-item flex-[0_0_calc(50%-0.5rem)]"
          down={
            currentRecord?.uptime
              ? formatUptime(currentRecord.uptime, t)
              : "-"
          }
        />
        <label className={`km-details-item flex flex-wrap gap-2 flex-[0_0_calc(50%-0.5rem)] ${align === "center" ? "justify-end" : ""}`}>
          <Flex align={"center"} gap="2">
            <Text size="2" weight="bold" wrap="nowrap">
              {t("nodeCard.last_updated")}
            </Text>
            <Text size="2">
              {node?.updated_at
                ? new Date(
                  currentRecord?.updated_at ||
                  node.updated_at
                ).toLocaleString()
                : "-"}
            </Text>
          </Flex>
        </label>
      </div>
    </Container>
  );
};
