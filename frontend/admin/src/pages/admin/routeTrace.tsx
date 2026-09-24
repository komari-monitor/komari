import React from "react";
import { Button, Flex, Select, Text, TextField } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import Loading from "@/components/loading";
import { NodeDetailsProvider, useNodeDetails } from "@/contexts/NodeDetailsContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { updateSettingsWithToast, useSettings } from "@/lib/api";

type RouteDiagnostic = {
  uuid: string;
  task_id: number;
  family: string;
  label: string;
  status: string;
  checked_at: string;
  target: string;
  resolved_ip: string;
  attempts: number;
  error?: string;
  hops: { ttl: number; address?: string; country?: string; asns?: string[] }[];
  samples?: string[][];
};

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
  const [familyServer, setFamilyServer] = React.useState("");
  const [families, setFamilies] = React.useState<Record<string, "ipv4" | "ipv6" | "both">>({});
  const [server, setServer] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [diagnostics, setDiagnostics] = React.useState<RouteDiagnostic[]>([]);
  const [diagnosticError, setDiagnosticError] = React.useState("");

  const refreshDiagnostics = React.useCallback(async () => {
    try {
      setDiagnostics(await call<Record<string, never>, RouteDiagnostic[]>("admin:getRouteDiagnostics", {}));
      setDiagnosticError("");
    } catch (cause) {
      setDiagnosticError(String(cause));
    }
  }, [call]);

  React.useEffect(() => {
    void refreshDiagnostics();
    const timer = window.setInterval(() => void refreshDiagnostics(), 10_000);
    return () => window.clearInterval(timer);
  }, [refreshDiagnostics]);

  const selectedDiagnostics = diagnostics
    .filter((item) => item.uuid === server)
    .sort((left, right) => right.checked_at.localeCompare(left.checked_at));

  React.useEffect(() => {
    if (settings) {
      setTarget(String(settings.route_trace_target ?? ""));
      setHours(String(settings.route_trace_interval_hours ?? 6));
      try {
        setFamilies(JSON.parse(String(settings.route_trace_families || "{}")));
      } catch {
        setFamilies({});
      }
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
      await updateSettingsWithToast({ route_trace_target: target.trim(), route_trace_interval_hours: interval, route_trace_families: JSON.stringify(families) }, t);
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
      <Flex direction="column" gap="2">
        <Text as="label" weight="medium">{t("routeTrace.familyServer")}</Text>
        <Flex gap="3" align="center" wrap="wrap">
          <Select.Root value={familyServer} onValueChange={setFamilyServer}>
            <Select.Trigger placeholder={t("routeTrace.selectServer")} />
            <Select.Content className="km-route-trace-select-content" position="popper">
              {nodeDetail.map((node) => <Select.Item key={node.uuid} value={node.uuid}>{node.name}</Select.Item>)}
            </Select.Content>
          </Select.Root>
          <Select.Root disabled={!familyServer} value={families[familyServer] || "both"} onValueChange={(value) => setFamilies((previous) => ({ ...previous, [familyServer]: value as "ipv4" | "ipv6" | "both" }))}>
            <Select.Trigger />
            <Select.Content position="popper">
              <Select.Item value="ipv4">IPv4</Select.Item>
              <Select.Item value="ipv6">IPv6</Select.Item>
              <Select.Item value="both">IPv4 + IPv6</Select.Item>
            </Select.Content>
          </Select.Root>
        </Flex>
        <Text size="2" color="gray">{t("routeTrace.familyHint")}</Text>
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
      <Flex direction="column" gap="3">
        <Flex gap="3" align="center">
          <Text size="5" weight="bold">{t("routeTrace.diagnostics")}</Text>
          <Button variant="soft" size="1" onClick={() => void refreshDiagnostics()}>{t("routeTrace.refresh")}</Button>
        </Flex>
        <Text size="2" color="gray">{t("routeTrace.diagnosticsHint")}</Text>
        {diagnosticError && <Text color="red" size="2">{diagnosticError}</Text>}
        {!server && <Text color="gray" size="2">{t("routeTrace.selectServer")}</Text>}
        {server && selectedDiagnostics.length === 0 && <Text color="gray" size="2">{t("routeTrace.noDiagnostics")}</Text>}
        {selectedDiagnostics.map((item) => (
          <div key={`${item.task_id}:${item.family}`} className="rounded-lg border border-gray-500/30 p-3">
            <Text weight="bold">#{item.task_id} · {item.family.toUpperCase()} · {item.label || t("common.unknown")}</Text>
            <Text as="p" size="2" color="gray">{item.target} → {item.resolved_ip || "—"} · {item.attempts} {t("routeTrace.passes")} · {new Date(item.checked_at).toLocaleString()}</Text>
            {item.error && <Text as="p" size="2" color="red">{item.error}</Text>}
            <div className="mt-2 max-h-64 overflow-auto rounded bg-black/20 p-2 font-mono text-xs">
              {item.hops.map((hop) => (
                <div key={hop.ttl}>{String(hop.ttl).padStart(2, "0")}　{hop.address || "*"}　{hop.country || ""}　{hop.asns?.map((asn) => `AS${asn}`).join(", ") || ""}</div>
              ))}
            </div>
            {(item.samples?.length || 0) > 1 && (
              <details className="mt-2 text-xs">
                <summary className="cursor-pointer">{t("routeTrace.rawPasses")}</summary>
                <div className="mt-2 max-h-64 overflow-auto rounded bg-black/20 p-2 font-mono">
                  {item.samples?.map((sample, pass) => (
                    <div key={pass} className="mb-2">
                      <div>{t("routeTrace.passNumber", { number: pass + 1 })}</div>
                      {sample.map((address, ttl) => <div key={ttl}>{String(ttl + 1).padStart(2, "0")}　{address || "*"}</div>)}
                    </div>
                  ))}
                </div>
              </details>
            )}
          </div>
        ))}
      </Flex>
    </Flex>
  );
}
