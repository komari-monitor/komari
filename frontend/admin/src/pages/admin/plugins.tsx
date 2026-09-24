import { useCallback, useEffect, useRef, useState } from "react";
import {
  Badge,
  Box,
  Button,
  Callout,
  Card,
  Dialog,
  Flex,
  Heading,
  Separator,
  Switch,
  Text,
} from "@radix-ui/themes";
import {
  Blocks,
  FileText,
  RefreshCw,
  Settings2,
  Trash2,
  Upload,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import Loading from "@/components/loading";
import InlineSvgIcon from "@/components/InlineSvgIcon";
import UploadDialog from "@/components/UploadDialog";
import { createChunkUploadTask, type ChunkUploadTask } from "@/lib/chunkUpload";
import { useAdminNavigation } from "@/contexts/AdminNavigationContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { resolveI18nText, type I18nText } from "@/utils/i18nText";
import { iconMap, resolvePluginIcon } from "@/utils/iconHelper";
import type { PluginInfo } from "@/types/plugin";

interface SetEnabledResult {
  requires_approval?: boolean;
}

// 插件是否声明了可编辑配置项（忽略 title 分组项）。
const hasConfiguration = (plugin: PluginInfo) =>
  Array.isArray(plugin.configuration?.data) &&
  plugin.configuration!.data!.some((item) => item.type !== "title");

// 渲染插件 icon：lucide 名用组件，URL/相对路径用图片或内联 SVG，否则默认 Blocks。
const renderPluginIcon = (
  plugin: PluginInfo,
  size: number,
  className: string,
) => {
  const icon = resolvePluginIcon(plugin.short, plugin.icon);
  if (!icon) return <Blocks size={size} className={className} />;
  const Cmp = iconMap[icon];
  if (Cmp) {
    return <Cmp size={size} className={className} />;
  }
  return (
    <InlineSvgIcon
      src={icon}
      alt=""
      className={`${className} object-contain`}
    />
  );
};

// 插件管理：上传 zip、switch 启停、权限确认弹窗、运行日志、行内错误文本。
export default function PluginsPage() {
  const { call } = useRPC2Call();
  const { refreshNavigation } = useAdminNavigation();
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const language = i18n.resolvedLanguage || i18n.language || "";
  const displayText = useCallback(
    (value: I18nText | undefined) => resolveI18nText(value, language) || "",
    [language],
  );

  const [plugins, setPlugins] = useState<PluginInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState(0);
  const uploadTaskRef = useRef<ChunkUploadTask | null>(null);
  const [uploadDialogOpen, setUploadDialogOpen] = useState(false);
  const [toggling, setToggling] = useState<string | null>(null);
  const [pendingApproval, setPendingApproval] = useState<PluginInfo | null>(
    null,
  );
  const [logPlugin, setLogPlugin] = useState<PluginInfo | null>(null);
  const [logContent, setLogContent] = useState("");
  const [logsLoading, setLogsLoading] = useState(false);
  const [pluginToDelete, setPluginToDelete] = useState<PluginInfo | null>(null);
  const [deleting, setDeleting] = useState(false);

  const loadList = useCallback(async () => {
    try {
      const result = await call<any, PluginInfo[]>("admin:listPlugins");
      setPlugins(Array.isArray(result) ? result : []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
  }, [call]);

  useEffect(() => {
    loadList().finally(() => setLoading(false));
  }, [loadList]);

  const uploadPlugin = async (file: File) => {
    if (!file.name.endsWith(".zip")) {
      toast.error(
        t(
          "plugin.invalid_file_type",
          "Invalid file type, only .zip files are supported",
        ),
      );
      return;
    }
    setUploading(true);
    setUploadProgress(0);
    const task = createChunkUploadTask("/api/admin/upload");
    uploadTaskRef.current = task;
    try {
      await task.upload("plugin", file, setUploadProgress);
      toast.success(t("plugin.uploaded", "Plugin uploaded"));
      setUploadDialogOpen(false);
      setUploadProgress(0);
      await loadList();
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError")) {
        toast.error(
          t("plugin.upload_failed", "Plugin upload failed") +
            ": " +
            (error instanceof Error ? error.message : String(error)),
        );
      }
    } finally {
      setUploading(false);
      uploadTaskRef.current = null;
    }
  };

  const cancelUpload = () => {
    uploadTaskRef.current?.cancel();
    setUploadProgress(0);
  };

  const toggle = async (plugin: PluginInfo, enabled: boolean) => {
    setToggling(plugin.short);
    try {
      const result = await call<
        { short: string; enabled: boolean },
        SetEnabledResult
      >("admin:setPluginEnabled", {
        short: plugin.short,
        enabled,
      });
      if (enabled && result?.requires_approval) {
        setPendingApproval(plugin);
        return;
      }
      toast.success(
        enabled
          ? t("plugin.enable_success", "Plugin enabled")
          : t("plugin.disable_success", "Plugin disabled"),
      );
      refreshNavigation();
      await loadList();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
      await loadList(); // 启动失败自动禁用，刷新后行内显示 last_error
    } finally {
      setToggling(null);
    }
  };

  const approveAndEnable = async () => {
    if (!pendingApproval) return;
    const plugin = pendingApproval;
    setPendingApproval(null);
    setToggling(plugin.short);
    try {
      await call("admin:setPluginEnabled", {
        short: plugin.short,
        enabled: true,
        approved: true,
      });
      refreshNavigation();
      toast.success(t("plugin.enable_success", "Plugin enabled"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    } finally {
      setToggling(null);
      await loadList();
    }
  };

  const openLogs = async (plugin: PluginInfo) => {
    setLogPlugin(plugin);
    setLogContent("");
    setLogsLoading(true);
    try {
      const result = await call<{ short: string }, { logs?: string }>(
        "admin:getPluginLogs",
        {
          short: plugin.short,
        },
      );
      setLogContent(result?.logs || "");
    } catch (error) {
      setLogContent(
        `${t("plugin.logs_load_failed", "Failed to load logs")}: ${
          error instanceof Error ? error.message : String(error)
        }`,
      );
    } finally {
      setLogsLoading(false);
    }
  };

  const deletePlugin = async (plugin: PluginInfo) => {
    setDeleting(true);
    try {
      await call("admin:deletePlugin", { short: plugin.short });
      toast.success(t("plugin.deleted", "Plugin deleted"));
      setPluginToDelete(null);
      await loadList();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    } finally {
      setDeleting(false);
    }
  };

  // 需要批准的权限列表（按能力拆分；超时/大小限制等运行时设置不在此列）。
  // server.registerRPC、server.getConfig、插件目录内文件读写默认授予，不弹确认。
  const permissionLabels = (plugin: PluginInfo): string[] => {
    const labels: string[] = [];
    const p = plugin.permissions;
    if (!p) return labels;
    if (p.allowSystemRPC)
      labels.push(t("plugin.permission.allowSystemRPC", "Call system RPC"));
    if (p.allowRoutes)
      labels.push(t("plugin.permission.allowRoutes", "Register HTTP routes"));
    if (p.allowHooks)
      labels.push(
        t("plugin.permission.allowHooks", "Modify HTTP requests/responses"),
      );
    if (p.allowHTMLInject)
      labels.push(
        t(
          "plugin.permission.allowHTMLInject",
          "Embed CSS/JS into every HTML page",
        ),
      );
    if (p.allowExec)
      labels.push(t("plugin.permission.allowExec", "Execute child processes"));
    if (p.allowListen)
      labels.push(t("plugin.permission.allowListen", "Listen on local ports"));
    if (p.allowAllFileAccess)
      labels.push(
        t(
          "plugin.permission.allowAllFileAccess",
          "Access files outside the plugin directory",
        ),
      );
    return labels;
  };

  if (loading) return <Loading />;

  return (
    <Box className="km-page-admin-plugins p-4 space-y-4">
      <Flex align="center" justify="between" className="km-plugins-toolbar">
        <Flex align="center" gap="2">
          <Blocks size={20} />
          <Heading size="4">{t("plugin.title", "Plugins")}</Heading>
        </Flex>
        <Flex gap="2">
          <Button
            onClick={() => setUploadDialogOpen(true)}
            disabled={uploading}
          >
            <Upload size={14} />
            {t("plugin.upload", "Upload Plugin")}
          </Button>
          <Button variant="soft" onClick={loadList}>
            <RefreshCw size={14} />
            {t("common.refresh", "Refresh")}
          </Button>
        </Flex>
      </Flex>

      <Separator size="4" />

      {plugins.length === 0 ? (
        <Callout.Root>
          <Callout.Text>
            {t("plugin.no_plugins", "No plugins installed yet")}
          </Callout.Text>
        </Callout.Root>
      ) : (
        <Flex direction="column" gap="3" className="km-plugins-list">
          {plugins.map((plugin) => (
            <Card key={plugin.short} className="km-plugin-card w-full">
              <Flex direction="column" gap="2">
                <Flex align="center" justify="between" gap="4">
                  <Flex align="center" gap="3" className="min-w-0">
                    {renderPluginIcon(plugin, 20, "h-5 w-5 shrink-0")}
                    <Flex direction="column" gap="1" className="min-w-0">
                      <Flex align="center" gap="2" wrap="wrap">
                        <Text weight="bold">
                          {displayText(plugin.name) || plugin.short}
                        </Text>
                        <Badge color={plugin.running ? "green" : "gray"}>
                          {plugin.running
                            ? t("plugin.running", "Running")
                            : t("plugin.stopped", "Stopped")}
                        </Badge>
                        <Text size="2" color="gray">
                          v{plugin.version}
                          {displayText(plugin.author)
                            ? ` · ${displayText(plugin.author)}`
                            : ""}
                        </Text>
                      </Flex>
                      {displayText(plugin.description) && (
                        <Text size="2" color="gray" className="truncate">
                          {displayText(plugin.description)}
                        </Text>
                      )}
                    </Flex>
                  </Flex>
                  <Flex align="center" gap="3" className="shrink-0">
                    {hasConfiguration(plugin) && (
                      <Button
                        size="1"
                        variant="soft"
                        onClick={() =>
                          navigate(
                            `/admin/plugins/config?short=${encodeURIComponent(plugin.short)}`,
                          )
                        }
                      >
                        <Settings2 size={14} />
                        {t("plugin.config", "Configuration")}
                      </Button>
                    )}
                    <Button
                      size="1"
                      variant="soft"
                      onClick={() => openLogs(plugin)}
                    >
                      <FileText size={14} />
                      {t("logs.title", "Logs")}
                    </Button>
                    <Button
                      size="1"
                      variant="ghost"
                      color="red"
                      onClick={() => setPluginToDelete(plugin)}
                      title={t("plugin.delete", "Delete plugin")}
                      aria-label={t("plugin.delete", "Delete plugin")}
                    >
                      <Trash2 size={14} />
                    </Button>
                    <Switch
                      checked={plugin.enabled}
                      disabled={toggling === plugin.short}
                      onCheckedChange={(checked) => toggle(plugin, checked)}
                      aria-label={
                        plugin.enabled
                          ? t("plugin.disable", "Disable plugin")
                          : t("plugin.enable", "Enable plugin")
                      }
                      title={
                        plugin.enabled
                          ? t("plugin.disable", "Disable plugin")
                          : t("plugin.enable", "Enable plugin")
                      }
                    />
                  </Flex>
                </Flex>
                {plugin.last_error && (
                  <Callout.Root color="red" size="1">
                    <Callout.Text>
                      {t("plugin.last_error", "Last error")}:{" "}
                      {plugin.last_error}
                    </Callout.Text>
                  </Callout.Root>
                )}
              </Flex>
            </Card>
          ))}
        </Flex>
      )}

      {/* 权限确认弹窗 */}
      <Dialog.Root
        open={pendingApproval !== null}
        onOpenChange={(open) => {
          if (!open) setPendingApproval(null);
        }}
      >
        <Dialog.Content>
          <Dialog.Title>
            {t("plugin.permission_title", "Permission required")}
          </Dialog.Title>
          <Text as="p" size="2" className="mb-2">
            {t(
              "plugin.permission_description",
              "This plugin requests the following permissions:",
            )}
          </Text>
          <Flex direction="column" gap="2" my="3">
            {pendingApproval &&
              permissionLabels(pendingApproval).length === 0 && (
                <Text size="2" color="gray">
                  {t(
                    "plugin.permission_none",
                    "This plugin requests no special permissions",
                  )}
                </Text>
              )}
            {pendingApproval &&
              permissionLabels(pendingApproval).map((label) => (
                <Badge
                  key={label}
                  color="orange"
                  size="2"
                  className="justify-start"
                >
                  {label}
                </Badge>
              ))}
          </Flex>
          <Text size="1" color="gray" className="mb-4">
            {t(
              "plugin.permission_default_note",
              "Default granted: read plugin configuration, register plugin RPC, read/write files inside the plugin directory",
            )}
          </Text>
          <Flex gap="2" justify="end">
            <Button variant="soft" onClick={() => setPendingApproval(null)}>
              {t("common.cancel", "Cancel")}
            </Button>
            <Button onClick={approveAndEnable}>
              {t("plugin.permission_approve", "Approve & Enable")}
            </Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>

      {/* 运行日志弹窗 */}
      <Dialog.Root
        open={logPlugin !== null}
        onOpenChange={(open) => {
          if (!open) setLogPlugin(null);
        }}
      >
        <Dialog.Content>
          <Dialog.Title>
            {t("logs.title", "Logs")} · {logPlugin?.short}
          </Dialog.Title>
          <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap rounded-md bg-gray-1 p-3 text-xs">
            {logsLoading
              ? t("plugin.loading", "Loading...")
              : logContent || t("plugin.no_logs", "No logs")}
          </pre>
        </Dialog.Content>
      </Dialog.Root>

      {/* 上传对话框 */}
      <UploadDialog
        open={uploadDialogOpen}
        onOpenChange={setUploadDialogOpen}
        title={t("plugin.upload", "Upload Plugin")}
        description={t(
          "plugin.upload_description",
          "Upload a .zip plugin package",
        )}
        accept=".zip"
        dragDropText={t("plugin.drag_drop", "Drag and drop files here")}
        clickToBrowseText={t(
          "plugin.or_click_to_browse",
          "Or click to browse files",
        )}
        hintText={t("plugin.zip_files_only", "Only .zip files are supported")}
        uploading={uploading}
        progress={uploadProgress}
        uploadingText={t("plugin.uploading", "Uploading...")}
        cancelUploadLabel={t("common.cancel", "Cancel")}
        onCancelUpload={cancelUpload}
        onFileSelected={uploadPlugin}
        closeLabel={t("common.cancel", "Cancel")}
      />

      {/* 删除确认 */}
      <Dialog.Root
        open={pluginToDelete !== null}
        onOpenChange={(open) => {
          if (!open) setPluginToDelete(null);
        }}
      >
        <Dialog.Content>
          <Dialog.Title>{t("plugin.delete", "Delete plugin")}</Dialog.Title>
          <Text as="p" size="2" my="3">
            {t("plugin.delete_confirm", "Delete this plugin?")}{" "}
            {pluginToDelete?.short}
            <br />
            {t(
              "plugin.delete_confirm_desc",
              "The plugin directory and its configuration will be removed.",
            )}
          </Text>
          <Flex gap="2" justify="end">
            <Button variant="soft" onClick={() => setPluginToDelete(null)}>
              {t("common.cancel", "Cancel")}
            </Button>
            <Button
              color="red"
              disabled={deleting}
              onClick={() => pluginToDelete && deletePlugin(pluginToDelete)}
            >
              {deleting
                ? t("plugin.deleting", "Deleting...")
                : t("plugin.delete", "Delete")}
            </Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </Box>
  );
}
