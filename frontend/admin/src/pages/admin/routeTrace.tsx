import React from "react";
import { Button, Flex, Select, Text, TextField } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import Loading from "@/components/loading";
import { NodeDetailsProvider, useNodeDetails } from "@/contexts/NodeDetailsContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { updateSettingsWithToast, useSettings } from "@/lib/api";

export default function RouteTrace() {
  return <NodeDetailsProvider><RouteTraceSettings /></NodeDetailsProvider>;
}

function RouteTraceSettings() {
  const { t } = useTranslation();
  const { settings, loading, error } = useSettings();
  const { nodeDetail, isLoading: nodesLoading, error: nodesError } = useNodeDetails();
  const { call } = useRPC2Call();
  const [target, setTarget] = React.useState("");
  const [hours, setHours] = React.useState("6");
  const [server, setServer] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (settings) {
      setTarget(String(settings.route_trace_target ?? ""));
      setHours(String(settings.route_trace_interval_hours ?? 6));
    }
  }, [settings]);

  if (loading || nodesLoading) return <Loading />;
  if (error || nodesError) return <Text color="red">{error || nodesError}</Text>;

  const save = async () => {
    const interval = Number(hours);
    if (!Number.isInteger(interval) || interval < 1 || interval > 168) {
      toast.error(t("routeTrace.intervalError"));
      return;
    }
    setBusy(true);
    try {
      await updateSettingsWithToast({ route_trace_target: target.trim(), route_trace_interval_hours: interval }, t);
    } finally {
      setBusy(false);
    }
  };

  const trigger = async () => {
    if (!server) return;
    setBusy(true);
    try {
      const result = await call<{ uuid: string }, { dispatched: number }>("admin:traceRoutes", { uuid: server });
      toast[result.dispatched > 0 ? "success" : "warning"](
        result.dispatched > 0 ? t("routeTrace.dispatched", { count: result.dispatched }) : t("routeTrace.noEligibleTask")
      );
    } catch (cause) {
      toast.error(String(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Flex direction="column" gap="4" className="p-0 md:p-4">
      <Text size="5" weight="bold">{t("routeTrace.title")}</Text>
      <Text color="gray">{t("routeTrace.description")}</Text>
      <Flex direction="column" gap="2">
        <Text as="label" weight="medium">{t("routeTrace.target")}</Text>
        <TextField.Root value={target} onChange={(event) => setTarget(event.target.value)} placeholder={t("routeTrace.targetPlaceholder")} />
        <Text size="2" color="gray">{t("routeTrace.targetHint")}</Text>
      </Flex>
      <Flex direction="column" gap="2">
        <Text as="label" weight="medium">{t("routeTrace.interval")}</Text>
        <TextField.Root type="number" min="1" max="168" step="1" value={hours} onChange={(event) => setHours(event.target.value)} />
      </Flex>
      <Button disabled={busy} onClick={save}>{t("routeTrace.save")}</Button>
      <Text size="5" weight="bold">{t("routeTrace.manual")}</Text>
      <Flex gap="3" align="center" wrap="wrap">
        <Select.Root value={server} onValueChange={setServer}>
          <Select.Trigger placeholder={t("routeTrace.selectServer")} />
          <Select.Content className="km-route-trace-select-content" position="popper">
            {nodeDetail.map((node) => <Select.Item key={node.uuid} value={node.uuid}>{node.name}</Select.Item>)}
          </Select.Content>
        </Select.Root>
        <Button disabled={busy || !server} onClick={trigger}>{t("routeTrace.runNow")}</Button>
      </Flex>
      <Text size="2" color="gray">{t("routeTrace.manualHint")}</Text>
    </Flex>
  );
}
