import React from "react";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import { useTranslation } from "react-i18next";
import Selector from "./Selector";

interface NodeSelectorProps {
  className?: string;
  hiddenDescription?: boolean;
  value: string[]; // uuid 列表
  onChange: (uuids: string[]) => void;
  hiddenUuidOnlyClient?: boolean;
}

const NodeSelector: React.FC<NodeSelectorProps> = ({
  className = "",
  hiddenDescription = false,
  value,
  onChange,
  hiddenUuidOnlyClient = false,
}) => {
  const { nodeDetail, isLoading, error } = useNodeDetails();
  const { t } = useTranslation();
  let nodesFiltered = value;
  if (hiddenUuidOnlyClient) {
    nodesFiltered = nodesFiltered.filter((node) =>
      nodeDetail.find((n) => n.uuid === node && !n.is_only_client)
    );
  }
  if (isLoading) return <div>Loading...</div>;
  if (error) return <div>{error}</div>;

  return (
    <Selector
      className={`km-node-selector ${className}`}
      hiddenDescription={hiddenDescription}
      value={nodesFiltered}
      onChange={onChange}
      items={[...nodeDetail]}
      sortItems={(a, b) => (a.weight ?? 0) - (b.weight ?? 0)}
      getId={(n) => n.uuid}
      getLabel={(n) => n.name}
      searchPlaceholder={t("common.search")}
      headerLabel={t("common.server")}
    />
  );
};

export default NodeSelector;
