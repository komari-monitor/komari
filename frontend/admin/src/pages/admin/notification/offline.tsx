import { Checkbox } from "@/components/ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  NodeDetailsProvider,
  useNodeDetails,
} from "@/contexts/NodeDetailsContext";
import {
  OfflineNotificationProvider,
  useOfflineNotification,
  type OfflineNotification,
} from "@/contexts/NotificationContext";
import React from "react";
import { Pencil, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Button,
  Dialog,
  Flex,
  IconButton,
  Switch,
  TextField,
} from "@radix-ui/themes";
import { toast } from "sonner";
import Loading from "@/components/loading";
import Tips from "@/components/ui/tips";

const OfflinePage = () => {
  return (
    <OfflineNotificationProvider>
      <NodeDetailsProvider>
        <InnerLayout />
      </NodeDetailsProvider>
    </OfflineNotificationProvider>
  );
};
const NotificationEditForm = ({
  initialValues,
  onSubmit,
  loading,
  onCancel,
}: {
  initialValues: { enable: boolean; cooldown: number; grace_period: number };
  onSubmit: (values: {
    enable: boolean;
    cooldown: number;
    grace_period: number;
  }) => void;
  loading?: boolean;
  onCancel?: () => void;
}) => {
  const { t } = useTranslation();
  const [enabled, setEnabled] = React.useState(initialValues.enable);
  // const [cooldown, setCooldown] = React.useState(initialValues.cooldown);
  const [grace, setGrace] = React.useState(initialValues.grace_period);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit({ enable: enabled, cooldown: 3000, grace_period: grace });
      }}
      className="km-notification-offline-form flex flex-col gap-2"
    >
      <label htmlFor="status">{t("common.status")}</label>
      <Switch
        id="status"
        name="status"
        checked={enabled}
        onCheckedChange={setEnabled}
      />
      {/* <label htmlFor="cooldown">{t("notification.offline.cooldown")}</label>
      <TextField.Root
        type="number"
        min={0}
        value={cooldown}
        onChange={e => setCooldown(Number(e.target.value))}
        id="cooldown"
        name="cooldown"
      /> */}
      <label htmlFor="grace_period" className="flex items-center gap-2">
        {t("notification.offline.grace_period")}<Tips>{t("notification.offline.grace_period_tip")}</Tips>
      </label>
      <TextField.Root
        type="number"
        min={0}
        value={grace}
        onChange={(e) => setGrace(Number(e.target.value))}
        id="grace_period"
        name="grace_period"
      />
      <Flex gap="2" justify="end" className="mt-4">
        {onCancel && (
          <Dialog.Close>
            <Button
              variant="soft"
              color="gray"
              type="button"
              onClick={onCancel}
            >
              {t("common.cancel")}
            </Button>
          </Dialog.Close>
        )}
        <Button variant="solid" type="submit" disabled={loading}>
          {t("common.save")}
        </Button>
      </Flex>
    </form>
  );
};

const InnerLayout = () => {
  const [search, setSearch] = React.useState("");
  const [selected, setSelected] = React.useState<string[]>([]);
  const {
    loading: onLoading,
    error: onError,
    offlineNotification,
    refresh,
  } = useOfflineNotification();
  const { isLoading: onNodeLoading, error: onNodeError } = useNodeDetails();
  const { t } = useTranslation();
  const [batchLoading, setBatchLoading] = React.useState(false);
  const [batchDialogOpen, setBatchDialogOpen] = React.useState(false);
  const [batchForm, setBatchForm] = React.useState({
    enable: true,
    cooldown: 1800,
    grace_period: 300,
  });

  // 批量修改
  const handleBatchEdit = (values: {
    enable: boolean;
    cooldown: number;
    grace_period: number;
  }) => {
    setBatchLoading(true);
    const payload = selected.map((id) => ({
      client: id,
      enable: values.enable,
      cooldown: values.cooldown,
      grace_period: values.grace_period,
    }));
    fetch("/api/admin/notification/offline/edit", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    })
      .then((res) => {
        if (!res.ok) {
          toast.error(
            "Failed to update offline notifications: " + res.statusText
          );
        } else {
          toast.success(t("common.updated_successfully"));
        }
        return res.json();
      })
      .then(() => {
        setBatchLoading(false);
        setBatchDialogOpen(false);
        refresh();
      })
      .catch((error) => {
        console.error("Error updating offline notifications:", error);
        toast.error(t("common.error", { message: error.message }));
        setBatchLoading(false);
      });
  };

  if (onLoading || onNodeLoading) {
    return <Loading text="(o゜▽゜)o☆" />;
  }
  if (onError || onNodeError) {
    return <div>Error: {onError?.message || onNodeError}</div>;
  }
  return (
    <div className="km-page-admin-notification-offline flex flex-col gap-4 md:p-4 p-1">
      <Flex justify="between" align="center" wrap="wrap">
        <label className="text-2xl font-semibold">
          {t("notification.offline.full_title", "离线通知设置")}
        </label>
        <TextField.Root
          type="text"
          className="max-w-64"
          placeholder={t("common.search")}
          value={search}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
            setSearch(e.target.value)
          }
        >
          <TextField.Slot>
            <Search size={16} />
          </TextField.Slot>
        </TextField.Root>
      </Flex>
      <OfflineNotificationTable
        search={search}
        selected={selected}
        onSelectionChange={setSelected}
      />
      <label className="text-sm text-muted-foreground">
        {t("common.selected", {
          count: selected.length,
        })}
      </label>
      <Flex gap="2" align="center">
        <Dialog.Root open={batchDialogOpen} onOpenChange={setBatchDialogOpen}>
          <Dialog.Trigger>
            <Button
              variant="soft"
              onClick={() => {
                // 默认取第一个选中项的配置作为初始值
                const first = offlineNotification.find(
                  (n) => n.client === selected[0]
                );
                setBatchForm({
                  enable: first?.enable ?? true,
                  cooldown: first?.cooldown ?? 1800,
                  grace_period: first?.grace_period ?? 300,
                });
              }}
              disabled={batchLoading || selected.length === 0}
            >
              {t("common.batch_edit")}
            </Button>
          </Dialog.Trigger>
          <Dialog.Content>
            <Dialog.Title>{t("common.batch_edit")}</Dialog.Title>
            <NotificationEditForm
              initialValues={batchForm}
              loading={batchLoading}
              onSubmit={handleBatchEdit}
              onCancel={() => setBatchDialogOpen(false)}
            />
          </Dialog.Content>
        </Dialog.Root>
      </Flex>
      <label className="km-notification-offline-preview text-sm text-muted-foreground">
        <span
          dangerouslySetInnerHTML={{ __html: t("notification.offline.tips") }}
        />
      </label>
    </div>
  );
};

const OfflineNotificationTable = ({
  search,
  selected,
  onSelectionChange,
}: {
  search: string;
  selected: string[];
  onSelectionChange: (ids: string[]) => void;
}) => {
  const { offlineNotification } = useOfflineNotification();
  const { nodeDetail } = useNodeDetails();
  const { t } = useTranslation();
  const filtered = [...nodeDetail]
    .sort((a, b) => a.weight - b.weight)
    .filter((node) => node.name.toLowerCase().includes(search.toLowerCase()));
  return (
    <div className="rounded-lg overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-6">
              <Checkbox
                checked={
                  selected.length === filtered.length
                    ? true
                    : selected.length > 0
                    ? "indeterminate"
                    : false
                }
                onCheckedChange={(checked) =>
                  onSelectionChange(checked ? filtered.map((n) => n.uuid) : [])
                }
              />
            </TableHead>
            <TableHead>{t("common.server")}</TableHead>
            <TableHead>{t("common.status")}</TableHead>
            {/* <TableHead>{t("notification.offline.cooldown")}</TableHead> */}
            <TableHead>{t("notification.offline.grace_period")}</TableHead>
            <TableHead>{t("notification.offline.last_notified")}</TableHead>
            <TableHead>{t("common.action")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {filtered.map((node) => (
            <TableRow key={node.uuid}>
              <TableCell>
                <Checkbox
                  checked={selected.includes(node.uuid)}
                  onCheckedChange={(checked) => {
                    if (checked) {
                      onSelectionChange([...selected, node.uuid]);
                    } else {
                      onSelectionChange(
                        selected.filter((id) => id !== node.uuid)
                      );
                    }
                  }}
                />
              </TableCell>
              <TableCell>{node.name}</TableCell>
              <TableCell>
                <Badge
                  color={
                    offlineNotification.find((n) => n.client === node.uuid)
                      ?.enable
                      ? "green"
                      : "red"
                  }
                >
                  {offlineNotification.find((n) => n.client === node.uuid)
                    ?.enable
                    ? t("common.enabled")
                    : t("common.disabled")}
                </Badge>
              </TableCell>
              {/* <TableCell>
                {offlineNotification.find((n) => n.client === node.uuid)
                  ?.cooldown || 1800}{" "}
                {t("nodeCard.time_second")}
              </TableCell> */}
              <TableCell>
                {offlineNotification.find((n) => n.client === node.uuid)
                  ?.grace_period || 300}
                {t("nodeCard.time_second")}
              </TableCell>
              <TableCell>
                {(() => {
                  const lastNotified = offlineNotification.find(
                    (n) => n.client === node.uuid
                  )?.last_notified;
                  if (!lastNotified) return "-";
                  const date = new Date(lastNotified);
                  if (date.getFullYear() < 3)
                    return t("notification.offline.never_triggered");
                  return date.toLocaleString();
                })()}
              </TableCell>
              <TableCell>
                <ActionButtons
                  offlineNotifications={offlineNotification.find(
                    (n) => n.client === node.uuid
                  )}
                />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
};

const ActionButtons = ({
  offlineNotifications,
}: {
  offlineNotifications: OfflineNotification | undefined;
}) => {
  const { t } = useTranslation();
  const { refresh } = useOfflineNotification();
  const [editOpen, setEditOpen] = React.useState(false);
  const [editSaving, setEditSaving] = React.useState(false);

  return (
    <Flex gap="2" align="center">
      <Dialog.Root open={editOpen} onOpenChange={setEditOpen}>
        <Dialog.Trigger>
          <IconButton
            variant="ghost"
            title={t("common.edit", "Edit")}
            aria-label={t("common.edit", "Edit")}
          >
            <Pencil size={16} />
          </IconButton>
        </Dialog.Trigger>
        <Dialog.Content>
          <Dialog.Title>{t("common.edit")}</Dialog.Title>
          <NotificationEditForm
            initialValues={{
              enable: offlineNotifications?.enable ?? false,
              cooldown: offlineNotifications?.cooldown ?? 1800,
              grace_period: offlineNotifications?.grace_period ?? 300,
            }}
            loading={editSaving}
            onSubmit={(values) => {
              setEditSaving(true);
              fetch("/api/admin/notification/offline/edit", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify([
                  {
                    client: offlineNotifications?.client,
                    ...values,
                  },
                ]),
              })
                .then((res) => {
                  if (!res.ok) {
                    toast.error(
                      "Failed to save offline notification settings: " +
                        res.statusText
                    );
                  }
                  toast.success(t("common.updated_successfully"));
                  return res.json();
                })
                .then(() => {
                  setEditOpen(false);
                  refresh();
                  setEditSaving(false);
                })
                .catch((error) => {
                  console.error(
                    "Error saving offline notification settings:",
                    error
                  );
                  toast.error(t("common.error", { message: error.message }));
                });
            }}
            onCancel={() => setEditOpen(false)}
          />
        </Dialog.Content>
      </Dialog.Root>
    </Flex>
  );
};

export default OfflinePage;
