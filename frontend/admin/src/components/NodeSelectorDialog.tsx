import React from "react";
import { Dialog, Button, Flex } from "@radix-ui/themes";
import NodeSelector from "./NodeSelector";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import { useTranslation } from "react-i18next";

interface NodeSelectorDialogProps {
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  value: string[];
  onChange: (uuids: string[]) => void;
  title?: React.ReactNode;
  className?: string;
  hiddenDescription?: boolean;
  hiddenUuidOnlyClient?: boolean;
  children?: React.ReactNode; // 新增 children 属性
}

const NodeSelectorDialog: React.FC<NodeSelectorDialogProps> = ({
  open: openProp,
  onOpenChange: onOpenChangeProp,
  value,
  onChange,
  title,
  className,
  hiddenDescription,
  hiddenUuidOnlyClient,
  children, // 解构 children
}) => {
  const { t } = useTranslation();
  const { nodeDetail } = useNodeDetails();
  // 自动/受控弹窗开关
  const [autoOpen, setAutoOpen] = React.useState(false);
  const open = openProp !== undefined ? openProp : autoOpen;
  const onOpenChange = onOpenChangeProp || setAutoOpen;
  // 临时选中，只有点击确定才提交
  const [temp, setTemp] = React.useState<string[]>(value ?? []);
  React.useEffect(() => {
    if (open) setTemp(value ?? []);
  }, [open, value]);

  const allUuids = React.useMemo(() => {
    const uuids = nodeDetail.map((n) => n.uuid);
    if (hiddenUuidOnlyClient) {
      return uuids.filter(
        (u) => !nodeDetail.find((n) => n.uuid === u)?.is_only_client
      );
    }
    return uuids;
  }, [nodeDetail, hiddenUuidOnlyClient]);
  const totalCount = allUuids.length;
  const isAllSelected =
    totalCount > 0 && allUuids.every((u) => temp.includes(u));

  const handleToggleAll = () => {
    if (isAllSelected) {
      setTemp(temp.filter((u) => !allUuids.includes(u)));
    } else {
      setTemp(Array.from(new Set([...temp, ...allUuids])));
    }
  };

  const handleOk = () => {
    onChange(temp);
    onOpenChange(false);
  };

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger>
        {children ? children : <Button>{title || t("common.select")}</Button>}
      </Dialog.Trigger>
      <Dialog.Content style={{ maxWidth: 400 }} className="km-node-selector-dialog">
        <Dialog.Title>{title || t("common.select")}</Dialog.Title>
        <Flex direction="column" gap="3">
          <Flex justify="between" align="center" gap="2">
            <label className="text-sm text-gray-600">
              {t("common.selected_total", {
                count: temp.length,
                total: totalCount,
              })}
            </label>
            <Button
              type="button"
              variant="soft"
              size="1"
              onClick={handleToggleAll}
              disabled={totalCount === 0}
            >
              {isAllSelected
                ? t("common.deselect_all")
                : t("common.select_all")}
            </Button>
          </Flex>
          <NodeSelector
            value={temp}
            onChange={setTemp}
            className={`km-node-selector-list ${className ?? ""}`}
            hiddenUuidOnlyClient={hiddenUuidOnlyClient}
            hiddenDescription={hiddenDescription}
          />
          <Flex justify="end" gap="2">
            <Dialog.Close>
              <Button variant="soft">{t("common.cancel")}</Button>
            </Dialog.Close>
            <Button onClick={handleOk}>{t("common.done")}</Button>
          </Flex>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
};

export default NodeSelectorDialog;
