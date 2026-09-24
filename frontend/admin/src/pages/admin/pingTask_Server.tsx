import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import { usePingTask, type PingTask } from "@/contexts/PingTaskContext";
import { Button, Dialog, Flex, IconButton } from "@radix-ui/themes";
import { MoreHorizontal } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Selector } from "@/components/Selector";
// 服务器视图：按服务器聚合展示其绑定的任务，并可快速增删绑定
export const ServerView = ({ pingTasks }: { pingTasks: PingTask[] }) => {
  const { t } = useTranslation();
  const { nodeDetail } = useNodeDetails();

  const sortedNodes = React.useMemo(
    () =>
      [...nodeDetail].sort((a, b) => {
        const wa = a.weight ?? 0;
        const wb = b.weight ?? 0;
        if (wa !== wb) return wa - wb;
        return a.name.localeCompare(b.name);
      }),
    [nodeDetail]
  );

  return (
    <div className="km-page-admin-pingtask-server km-pingtask-server-list rounded-xl overflow-hidden">
      <Table>
        <TableHeader>
          <TableHead className="w-48">{t("common.server")}</TableHead>
          <TableHead>{t("ping.task")}</TableHead>
        </TableHeader>
        <TableBody>
          {sortedNodes.map((n) => (
            <ServerRow
              key={n.uuid}
              nodeUuid={n.uuid}
              nodeName={n.name}
              pingTasks={pingTasks}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  );
};

const ServerRow: React.FC<{
  nodeUuid: string;
  nodeName: string;
  pingTasks: PingTask[];
}> = ({ nodeUuid, nodeName, pingTasks }) => {
  const { t } = useTranslation();
  const { refresh } = usePingTask();
  const [open, setOpen] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  // 当前服务器拥有的任务集合
  const ownedTasks = React.useMemo(
    () => pingTasks.filter((t) => t.clients?.includes(nodeUuid)),
    [pingTasks, nodeUuid]
  );

  // 编辑状态（所选任务 id 集合）
  const [selectedIds, setSelectedIds] = React.useState<string[]>(
    () => ownedTasks.filter((t) => t.id !== undefined).map((t) => String(t.id))
  );

  // 若任务或服务器改变，重置选择
  React.useEffect(() => {
    setSelectedIds(
      ownedTasks.filter((t) => t.id !== undefined).map((t) => String(t.id))
    );
  }, [ownedTasks]);

  const handleSave = () => {
    setSaving(true);
    // 收集需要更新的任务（ membership 发生变化 ）
    const toUpdate = pingTasks
      .filter((task) => task.id !== undefined)
      .filter((task) => {
        const hasBefore = !!task.clients?.includes(nodeUuid);
        const hasAfter = selectedIds.includes(String(task.id));
        return hasBefore !== hasAfter; // 仅当变化才提交
      })
      .map((task) => {
        const hasAfter = selectedIds.includes(String(task.id));
        const current = new Set(task.clients || []);
        if (hasAfter) current.add(nodeUuid);
        else current.delete(nodeUuid);
        return {
          id: task.id,
          name: task.name,
          type: task.type,

          target: task.target!,
          default_on: task.default_on || false,
          clients: Array.from(current),
          interval: task.interval,
        };
      });

    if (toUpdate.length === 0) {
      setOpen(false);
      setSaving(false);
      return;
    }

    fetch("/api/admin/ping/edit", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tasks: toUpdate }),
    })
      .then((res) => {
        if (!res.ok)
          return res.json().then((d) => {
            throw new Error(d?.message || t("common.error"));
          });
        return res.json();
      })
      .then(() => {
        toast.success(t("common.updated_successfully"));
        setOpen(false);
        refresh();
      })
      .catch((e) => toast.error(e.message))
      .finally(() => setSaving(false));
  };

  const joined = ownedTasks.map((t) => t.name).join(", ");
  const display = joined.length > 40 ? joined.slice(0, 40) + "..." : joined;

  return (
    <TableRow>
      <TableCell>{nodeName}</TableCell>
      <TableCell>
        <Flex align="center" gap="2">
          {ownedTasks.length > 0 ? display : t("common.none")}
          <Dialog.Root open={open} onOpenChange={setOpen}>
            <Dialog.Trigger>
              <IconButton
                variant="ghost"
                title={t("common.select_tasks", "Select tasks")}
                aria-label={t("common.select_tasks", "Select tasks")}
              >
                <MoreHorizontal size={16} />
              </IconButton>
            </Dialog.Trigger>
            <Dialog.Content maxWidth="450px" className="km-pingtask-server-form">
              <Dialog.Title>
                {t("common.server")} - {nodeName}
              </Dialog.Title>
              <div className="mt-2">
                <Selector
                  value={selectedIds}
                  onChange={setSelectedIds}
                  items={[...pingTasks.filter((t) => t.id !== undefined)]}
                  getId={(task) => String(task.id)}
                  getLabel={(task) => (
                    <span className="text-sm">
                      {task.name}
                      {task.default_on && (
                        <span className="ml-2 text-xs text-accent-11">
                          {t("ping.default_on_short")}
                        </span>
                      )}
                      <span className="ml-2 text-xs text-gray-500">
                        {task.type}/{task.interval}s
                      </span>
                    </span>
                  )}
                  headerLabel={t("ping.task")}
                  searchPlaceholder={t("common.search", { defaultValue: "Search" })}
                  filterItem={(item, keyword) =>
                    String(item.name).toLowerCase().includes(keyword.toLowerCase())
                  }
                />
              </div>
              <Flex gap="2" justify="end" className="mt-4">
                <Dialog.Close>
                  <Button
                    variant="soft"
                    color="gray"
                    type="button"
                    onClick={() => setOpen(false)}
                  >
                    {t("common.cancel")}
                  </Button>
                </Dialog.Close>
                <Button onClick={handleSave} disabled={saving}>
                  {t("common.save")}
                </Button>
              </Flex>
            </Dialog.Content>
          </Dialog.Root>
        </Flex>
      </TableCell>
    </TableRow>
  );
};
