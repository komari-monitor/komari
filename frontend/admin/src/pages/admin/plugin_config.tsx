import { useCallback, useEffect, useMemo, useState } from "react";
import { Blocks } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import {
  Box,
  Button,
  Callout,
  Card,
  Flex,
  Heading,
  Text,
} from "@radix-ui/themes";

import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import Loading from "@/components/loading";
import InlineSvgIcon from "@/components/InlineSvgIcon";
import ConfigFormTabs from "@/components/admin/ConfigFormTabs";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { resolveI18nText } from "@/utils/i18nText";
import { iconMap, resolvePluginIcon } from "@/utils/iconHelper";
import type { I18nText } from "@/utils/i18nText";
import type { PluginConfiguration, PluginInfo, PluginConfigItem } from "@/types/plugin";

interface ConfigurationResponse {
  configuration?: PluginConfiguration;
  data?: Record<string, any>;
}

// 渲染插件 icon：lucide 名用组件，URL/相对路径用图片或内联 SVG，否则默认 Blocks。
const renderPluginIcon = (
  plugin: PluginInfo,
  size: number,
  className: string,
  opacity: number,
) => {
  const icon = resolvePluginIcon(plugin.short, plugin.icon);
  if (!icon) {
    return <Blocks size={size} className={className} style={{ opacity }} />;
  }
  const Cmp = iconMap[icon];
  if (Cmp) {
    return <Cmp size={size} className={className} style={{ opacity }} />;
  }
  return (
    <InlineSvgIcon
      src={icon}
      alt=""
      className={`${className} object-contain`}
      style={{ opacity }}
    />
  );
};

// 插件配置：左侧插件列表，右侧具体插件的配置项（与主题配置一致）。
export default function PluginConfigPage() {
  const { call } = useRPC2Call();
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language || "";

  const [searchParams] = useSearchParams();
  const [plugins, setPlugins] = useState<PluginInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<PluginInfo | null>(null);
  const [autoSelectDone, setAutoSelectDone] = useState(false);
  const [configuration, setConfiguration] = useState<PluginConfiguration | null>(null);
  const [values, setValues] = useState<Record<string, any>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const displayText = useCallback(
    (value: I18nText | undefined) => resolveI18nText(value, language) || "",
    [language],
  );

  // 插件是否声明了可展示配置项（忽略 title 分组项），无配置项的插件不显示在列表中。
  const hasConfiguration = (plugin: PluginInfo) =>
    Array.isArray(plugin?.configuration?.data) &&
    plugin!.configuration!.data!.some((item) => item.type !== "title");

  const configurablePlugins = useMemo(
    () => plugins.filter(hasConfiguration),
    [plugins],
  );

  const loadList = useCallback(async () => {
    try {
      const result = await call<any, PluginInfo[]>("admin:listPlugins");
      const nextPlugins = Array.isArray(result) ? result : [];
      setPlugins(nextPlugins);
      return nextPlugins;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return null;
    }
  }, [call]);

  useEffect(() => {
    loadList().finally(() => setLoading(false));
  }, [loadList]);


  const selectPlugin = useCallback(async (plugin: PluginInfo) => {
    setSelected(plugin);
    setConfiguration(null);
    setValues({});
    setError(null);
    try {
      const result = await call<any, ConfigurationResponse>("admin:getPluginConfiguration", {
        short: plugin.short,
      });
      const configuration = result?.configuration || {};
      setConfiguration(configuration);
      const items: PluginConfigItem[] = Array.isArray(configuration.data)
        ? configuration.data
        : [];
      const init: Record<string, any> = {};
      items.forEach((item) => {
        if (item.type !== "title" && item.type !== "textbox" && item.key) {
          const saved = result?.data?.[item.key];
          const selection = item.type === "nodes" || item.type === "pingtasks";
          init[item.key] =
            saved !== undefined
              ? selection
                ? JSON.stringify(saved)
                : saved
              : item.default ??
                (selection ? "[]" : undefined);
        }
      });
      setValues(init);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [call]);
  // 从插件管理页跳转过来时（?short=xxx）自动选中对应插件。
  useEffect(() => {
    if (loading || autoSelectDone) return;
    const target = searchParams.get("short");
    if (!target || configurablePlugins.length === 0) return;
    const plugin = configurablePlugins.find((item) => item.short === target);
    if (plugin) {
      setAutoSelectDone(true);
      selectPlugin(plugin);
    }
  }, [loading, autoSelectDone, searchParams, configurablePlugins, selectPlugin]);

  const handleValueChange = (key: string, value: any) => {
    setValues((current) => ({ ...current, [key]: value }));
  };

  const saveAll = async () => {
    if (!selected) return;
    setSaving(true);
    try {
      await call("admin:setPluginConfiguration", {
        short: selected.short,
        data: values,
      });
      toast.success(
        selected.enabled
          ? t(
              "plugin.config_saved_and_reloaded",
              "Configuration saved and plugin reloaded",
            )
          : t("plugin.config_saved", "Configuration saved"),
      );
    } catch (e) {
      const errorMessage = e instanceof Error ? e.message : String(e);
      const reloadFailed =
        selected.enabled && errorMessage.includes("plugin configuration saved but reload failed");
      toast.error(
        `${reloadFailed
          ? t(
              "plugin.config_saved_reload_failed",
              "Configuration saved, but plugin reload failed",
            )
          : t("plugin.config_save_failed", "Failed to save configuration")}: ${
          errorMessage
        }`,
      );
      if (reloadFailed) {
        const nextPlugins = await loadList();
        const nextSelected = nextPlugins?.find((plugin) => plugin.short === selected.short);
        if (nextSelected) setSelected(nextSelected);
      }
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <Loading />;

  const items: PluginConfigItem[] = Array.isArray(configuration?.data)
    ? configuration.data
    : [];
  const hasItems = items.some((item) => item.type !== "title");
  const hasEditableItems = items.some(
    (item) => item.type !== "title" && item.type !== "textbox",
  );

  return (
    <Box className="km-page-admin-plugin-config h-full min-h-0 p-4">
      <ConfigFormTabs
        items={items}
        values={values}
        onValueChange={handleValueChange}
        resolveText={displayText}
        className="h-full"
        header={
          <Flex direction="column" gap="3">
            <Flex justify="between" align="center" wrap="wrap" gap="3">
              <Flex align="center" gap="2">
                <Blocks size={20} />
                <Heading size="4">{t("plugin.config", "Configuration")}</Heading>
              </Flex>
              {selected && !error && hasEditableItems && (
                <Button onClick={saveAll} disabled={saving}>
                  {saving
                    ? selected.enabled
                      ? t("plugin.saving_and_reloading", "Saving and reloading...")
                      : t("plugin.saving", "Saving...")
                    : selected.enabled
                      ? t("plugin.save_and_reload", "Save and reload")
                      : t("common.save")}
                </Button>
              )}
            </Flex>
            {selected && !error && hasItems && (
              <Text weight="bold">
                {displayText(selected.name) || selected.short}
              </Text>
            )}
          </Flex>
        }
        notice={
          !selected || error || !hasItems ? (
            <Box className="mb-4">
              {!selected ? (
                <Callout.Root>
                  <Callout.Text>{t("plugin.config_select_hint", "Select a plugin to configure")}</Callout.Text>
                </Callout.Root>
              ) : error ? (
                <Callout.Root color="red">
                  <Callout.Text>{error}</Callout.Text>
                </Callout.Root>
              ) : (
                <Callout.Root>
                  <Callout.Text>{t("plugin.config_no_items", "This plugin has no configuration items")}</Callout.Text>
                </Callout.Root>
              )}
            </Box>
          ) : undefined
        }
        sidebar={
          <Card>
            <Flex direction="column" gap="1">
              {configurablePlugins.length === 0 && (
                <Text size="2" color="gray">
                  {plugins.length === 0
                    ? t("plugin.no_plugins", "No plugins installed yet")
                    : t("plugin.config_no_configurable_plugins", "No plugins with configuration items")}
                </Text>
              )}
              {configurablePlugins.map((plugin) => {
                const isActive = selected?.short === plugin.short;
                return (
                  <button
                    key={plugin.short}
                    type="button"
                    onClick={() => selectPlugin(plugin)}
                    className="group flex w-full items-center gap-2 rounded-md p-2 text-left transition-colors duration-200 hover:bg-accent-3"
                    style={{
                      borderLeft: isActive
                        ? "4px solid var(--accent-8)"
                        : "4px solid transparent",
                      backgroundColor: isActive ? "var(--accent-4)" : "transparent",
                      color: isActive ? "var(--accent-10)" : "inherit",
                    }}
                  >
                    {renderPluginIcon(plugin, 16, "h-5 w-5 shrink-0", isActive ? 1 : 0.7)}
                    <Text
                      size="2"
                      weight={isActive ? "bold" : "medium"}
                      className="min-w-0 truncate"
                    >
                      {displayText(plugin.name) || plugin.short}
                    </Text>
                  </button>
                );
              })}
            </Flex>
          </Card>
        }
      />
    </Box>
  );
}
