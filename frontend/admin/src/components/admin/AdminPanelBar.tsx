import { Cross1Icon, ExitIcon } from "@radix-ui/react-icons";
import {
  Button,
  Callout,
  Flex,
  Grid,
  IconButton,
  Text,
} from "@radix-ui/themes";
import { AnimatePresence, motion } from "framer-motion"; // 引入 Framer Motion
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation /*useNavigate*/ } from "react-router-dom";
import ColorSwitch from "../ColorSwitch";
import LanguageSwitch from "../Language";
import ThemeSwitch from "../ThemeSwitch";
import { useIsMobile } from "@/hooks/use-mobile";
import menuConfig from "../../config/menuConfig.json";
import type { MenuItem } from "../../types/menu";
import { iconMap, resolvePluginIcon } from "../../utils/iconHelper";
import { ChevronDownIcon } from "@radix-ui/react-icons";
import { TablerMenu2 } from "../Icones/Tabler";
import LoginDialog from "../Login";
import InlineSvgIcon from "../InlineSvgIcon";
import { useAdminNavigation } from "@/contexts/AdminNavigationContext";
import { useAccount } from "@/contexts/AccountContext";
import { usePublicInfo } from "@/contexts/PublicInfoContext";
import Tips from "../ui/tips";
import { CircleFadingArrowUp } from "lucide-react";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { resolveI18nText } from "@/utils/i18nText";
import type { PluginInfo } from "@/types/plugin";
import {
  normalizeThemeRedirectTarget,
} from "@/utils/themeConfiguration";

// 将JSON配置转换为类型安全的菜单项数组 (基础静态菜单)
const baseMenuItems = (menuConfig as { menu: MenuItem[] }).menu;

// 扩展的菜单项类型（允许直接提供 rawLabel 而不是多语言 key）
interface ExtendedMenuItem extends MenuItem {
  rawLabel?: string; // 不走 i18n，直接显示
  reloadDocument?: boolean;
}

interface AdminPanelBarProps {
  content: ReactNode;
}

const AdminPanelBar = ({ content }: AdminPanelBarProps) => {
  const { call } = useRPC2Call();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [openSubMenus, setOpenSubMenus] = useState<{ [key: string]: boolean }>({
    // 默认所有子菜单关闭
  });
  const { account } = useAccount();
  const isMobile = useIsMobile();
  const ishttps = window.location.protocol === "https:";
  const [t, i18n] = useTranslation();
  const location = useLocation();
  const isConfigFormPage =
    location.pathname === "/admin/settings/page" ||
    location.pathname === "/admin/plugins/config";
  const { publicInfo } = usePublicInfo();
  const { refreshVersion } = useAdminNavigation();
  //const navigate = useNavigate();
  // 获取版本信息
  const [versionInfo, setVersionInfo] = useState<{
    hash: string;
    version: string;
  } | null>(null);
  const currentLanguage =
    i18n.resolvedLanguage ||
    i18n.language ||
    (typeof navigator !== "undefined" ? navigator.language : "");
  // GitHub 最新发布信息与更新检测
  interface GithubReleaseInfo {
    tag_name: string;
    name?: string;
    body?: string;
    html_url: string;
    published_at?: string;
    draft?: boolean;
    prerelease?: boolean;
  }
  const [latestRelease, setLatestRelease] = useState<GithubReleaseInfo | null>(
    null,
  );
  const [updateAvailable, setUpdateAvailable] = useState(false);
  const [releasesSince, setReleasesSince] = useState<GithubReleaseInfo[]>([]);


  // 动态扩展菜单（主题 + 插件注入页面）
  const [pluginMenuItems, setPluginMenuItems] = useState<ExtendedMenuItem[]>([]);

  // 插件注入的管理页面：manifest pages（visibility=admin）-> 插件菜单的二级菜单。
  // iframe 页面进入 plugin-page 路由；redirect 页面复用主题的站内跳转校验。
  useEffect(() => {
    let ignore = false;
    async function loadPluginMenu() {
      try {
        const result = await call<any, PluginInfo[]>("admin:listPlugins");
        if (ignore || !Array.isArray(result)) return;
        const pluginIconUrl = (plugin: PluginInfo, icon?: string) =>
          resolvePluginIcon(plugin.short, icon) || "Blocks";
        const items: ExtendedMenuItem[] = [];
        for (const plugin of result) {
          if (!plugin.enabled) continue; // 未启用的插件不注入导航菜单
          for (const page of plugin.pages || []) {
            if (page.visibility === "public") continue; // 公开页面走公开路由，不进后台导航
            const label =
              resolveI18nText(page.title, currentLanguage) ||
              resolveI18nText(plugin.name, currentLanguage) ||
              plugin.short;
            const pageType = page.type || "iframe";
            if (pageType === "redirect") {
              const target = normalizeThemeRedirectTarget(page.url);
              if (!target) continue;
              items.push({
                labelKey: label,
                rawLabel: label,
                path: target,
                icon: pluginIconUrl(plugin, page.icon),
                reloadDocument: true, // 与主题 redirect 一致：整页跳转到站内路径
              });
              continue;
            }
            items.push({
              labelKey: label,
              rawLabel: label,
              path: `/admin/plugin-page?short=${encodeURIComponent(plugin.short)}&file=${encodeURIComponent(page.file || "")}`,
              icon: pluginIconUrl(plugin, page.icon),
            });
          }
        }
        if (!ignore) setPluginMenuItems(items);
      } catch (e) {
        console.warn("加载插件菜单失败:", e);
        if (!ignore) setPluginMenuItems([]);
      }
    }
    loadPluginMenu();
    return () => {
      ignore = true;
    };
  }, [call, currentLanguage, refreshVersion]);

  useEffect(() => {
    const fetchVersionInfo = async () => {
      try {
        //const response = await fetch("/api/version");
        const data = await call("common:getVersion");
        setVersionInfo({
          hash: data.hash?.slice(0, 7),
          version: data.version,
        });
      } catch (error) {
        console.error("Failed to fetch version info:", error);
      }
    };

    fetchVersionInfo();
  }, []);

  // 规范化版本为 [major, minor, patch] 数组，忽略前缀 v 和后缀
  function parseSemver(input?: string | null): number[] | null {
    if (!input) return null;
    const s = String(input).trim().replace(/^v/i, "");
    const match = s.match(/^(\d+)\.(\d+)\.(\d+)/);
    if (!match) return null;
    return [Number(match[1]), Number(match[2]), Number(match[3])];
  }

  function isNewerVersion(latest?: string | null, current?: string | null) {
    const a = parseSemver(latest);
    const b = parseSemver(current);
    if (!a || !b) return false;
    for (let i = 0; i < 3; i++) {
      if (a[i] > b[i]) return true;
      if (a[i] < b[i]) return false;
    }
    return false;
  }

  // 获取 GitHub releases 列表，并筛选出“比当前版本新的所有 release”
  useEffect(() => {
    let ignore = false;
    const currentVersion = (publicInfo as any)?.version || versionInfo?.version;
    if (!currentVersion) return;

    async function loadReleases() {
      try {
        const resp = await fetch(
          "https://api.github.com/repos/komari-monitor/komari/releases?per_page=100",
          {
            headers: {
              Accept: "application/vnd.github+json",
            },
            cache: "no-cache",
          },
        );
        if (!resp.ok) throw new Error(`GitHub HTTP ${resp.status}`);
        const data: GithubReleaseInfo[] = await resp.json();
        if (ignore) return;
        const valid = (data || [])
          .filter((r) => !r.draft && !r.prerelease)
          .filter((r) =>
            isNewerVersion(r?.tag_name || r?.name, currentVersion),
          );
        setReleasesSince(valid);
        setLatestRelease(valid.length ? valid[0] : null);
        setUpdateAvailable(valid.length > 0);
      } catch (e) {
        console.warn("加载 GitHub 最新发布失败:", e);
        if (!ignore) {
          setLatestRelease(null);
          setReleasesSince([]);
          setUpdateAvailable(false);
        }
      }
    }

    loadReleases();
    return () => {
      ignore = true;
    };
  }, [publicInfo, versionInfo]);
  // Handle responsive behavior
  useEffect(() => {
    const handleResize = () => setSidebarOpen(!isMobile);
    handleResize();
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, [isMobile]);

  // 主题配置和插件注入页面分别作为“主题”“插件”主菜单的二级菜单。
  const mergedBaseMenuItems: ExtendedMenuItem[] = useMemo(() => {
    return baseMenuItems.map((item) => {
      if (item.labelKey === "plugin.title" && pluginMenuItems.length > 0) {
        return {
          ...item,
          children: [...(item.children || []), ...pluginMenuItems],
        };
      }
      return item;
    });
  }, [pluginMenuItems]);
  const bottomStartPath = mergedBaseMenuItems.find(
    (item) => item.bottom,
  )?.path;

  // 根据路径自动展开子菜单（包含动态扩展项；plugin-page 用 query 定位文件，
  // 因此子菜单匹配基于 pathname 部分）
  useEffect(() => {
    const newState: { [key: string]: boolean } = {};
    const combined: ExtendedMenuItem[] = mergedBaseMenuItems;
    combined.forEach((item) => {
      if (item.children) {
        newState[item.path] = item.children.some((child: MenuItem) => {
          const childPath = child.path.split("?")[0];
          return (
            location.pathname === childPath ||
            (childPath !== "/" &&
              location.pathname.startsWith(childPath + "/"))
          );
        });
      }
    });
    setOpenSubMenus(newState);
  }, [location.pathname, mergedBaseMenuItems]);

  // 侧边栏动画变体
  const sidebarVariants = {
    open: {
      width: isMobile ? "100vw" : "240px",
      opacity: 1,
      transition: {
        type: "spring" as const,
        stiffness: 300,
        damping: 30,
      },
    },
    closed: {
      width: 0,
      opacity: isMobile ? 0 : 1, // 移动端完全透明
      transition: {
        type: "spring" as const,
        stiffness: 300,
        damping: 30,
      },
    },
  };

  // 内容区域动画变体
  const contentVariants = {
    open: {
      opacity: isMobile ? 0 : 1,
      x: isMobile ? "100%" : 0,
      transition: {
        duration: 0.3,
      },
    },
    closed: {
      opacity: 1,
      x: 0,
      transition: {
        duration: 0.3,
      },
    },
  };

  function logout() {
    window.open("/api/logout", "_self");
  }
  return (
    <>
      <Grid
        className="km-admin-layout km-admin-panel-bar"
        columns={{ initial: "1fr", md: sidebarOpen ? "240px 1fr" : "0px 1fr" }} // 动态调整网格列
        rows={{ initial: "auto 1fr", md: "auto 1fr" }}
        style={{
          height: "100vh",
          width: "100vw",
          overflow: "hidden",
          backgroundColor: "var(--accent-1)",
        }}
      >
        {/* Navbar */}
        <motion.nav
          className="km-admin-panel-topbar col-span-2"
          initial={{ y: 0 }}
          animate={{ y: 0 }}
          transition={{ duration: 0.5, ease: "easeOut" }}
        >
          <Flex
            gap="3"
            p="2"
            justify="between"
            align="center"
            className="border-b-1"
          >
            <Flex gap="3" align="center">
              <IconButton
                variant="ghost"
                onClick={() => setSidebarOpen(!sidebarOpen)}
                title={t("common.menu_sidebar", "Menu")}
                aria-label={t("common.menu_sidebar", "Menu")}
                style={{
                  display: isMobile && sidebarOpen ? "none" : "flex",
                  color: "var(--gray-11)",
                }}
              >
                <TablerMenu2 />
              </IconButton>
              <a href="/" target="_blank" rel="noopener noreferrer">
                <label className="text-xl font-bold">Komari</label>
              </a>
              {updateAvailable && releasesSince.length > 0 && (
                <Tips
                  mode="dialog"
                  className="check-update"
                  trigger={<CircleFadingArrowUp color="#FB4141" size="16" />}
                >
                  <div className="flex flex-col gap-2 max-w-[80vw] md:max-w-[720px]">
                    <label className="font-bold">
                      {t("common.update_available")}
                    </label>
                    <div className="text-sm text-muted-foreground">
                      <span style={{ marginRight: 8 }}>
                        {(publicInfo as any)?.version || versionInfo?.version}
                      </span>
                      <span>{"> "}</span>
                      <span>
                        {(latestRelease?.tag_name || latestRelease?.name) ?? ""}
                      </span>
                    </div>

                    <div className="rounded-md p-2 overflow-auto max-h-80">
                      <div className="flex flex-col gap-4 text-sm">
                        {releasesSince.map((r) => (
                          <div key={r.html_url} className="flex flex-col gap-2">
                            <div className="flex items-center justify-between">
                              <div className="font-medium">
                                {r.name || r.tag_name}
                              </div>
                              {r.published_at && (
                                <div className="text-xs text-muted-foreground">
                                  {new Date(r.published_at).toLocaleString()}
                                </div>
                              )}
                            </div>
                            <div className="whitespace-pre-wrap break-words">
                              {r.body || ""}
                            </div>
                            <div
                              style={{
                                height: 1,
                                background: "var(--accent-5)",
                                opacity: 0.5,
                              }}
                            />
                          </div>
                        ))}
                      </div>
                    </div>
                    <div className="flex justify-end">
                      <a
                        href={latestRelease?.html_url}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        <Button variant="soft">Github</Button>
                      </a>
                    </div>
                  </div>
                </Tips>
              )}
              <label
                className="text-sm text-muted-foreground self-end overflow-hidden"
                hidden={isMobile}
              >
                {(publicInfo as any)?.version ||
                  (versionInfo &&
                    `${versionInfo.version} (${versionInfo.hash})`)}
              </label>
            </Flex>
            <Flex gap="3" align="center" overflowX="auto" className="km-admin-panel-controls">
              {account && !account.logged_in && (
                <LoginDialog
                  autoOpen={true}
                  showSettings={false}
                  onLoginSuccess={() => {
                    window.location.reload();
                  }}
                />
              )}
              <ThemeSwitch />
              <ColorSwitch />
              <LanguageSwitch />
              <IconButton
                variant="soft"
                color="orange"
                className="km-admin-panel-account"
                onClick={logout}
                title={t("common.logout", "Logout")}
                aria-label={t("common.logout", "Logout")}
              >
                <ExitIcon />
              </IconButton>
            </Flex>
          </Flex>
        </motion.nav>

        {/* Sidebar */}
        <AnimatePresence>
          <motion.div
            variants={sidebarVariants}
            initial="closed"
            animate={sidebarOpen ? "open" : "closed"}
            exit="closed"
            className="km-admin-panel-nav"
            style={{
              backgroundColor: "var(--accent-1)",
              height: "100%",
              position: isMobile ? "absolute" : "relative",
              zIndex: isMobile ? 10 : 1,
              overflowY: "auto",
              overflowX: "hidden",
            }}
          >
            <Flex
              gap="3"
              className="p-2 border-r-1"
              direction="column"
              justify="start"
              align="start"
              style={{ height: "100%", minWidth: "240px" }}
            >
              {/* 关闭按钮 */}
              <IconButton
                variant="soft"
                title={t("common.close_sidebar", "Close menu")}
                aria-label={t("common.close_sidebar", "Close menu")}
                style={{
                  display: isMobile ? "flex" : "none",
                  margin: "8px 0px 0px 8px",
                }}
                onClick={() => setSidebarOpen(false)}
              >
                <Cross1Icon />
              </IconButton>
              {/* 侧边连链接 */}
              <Flex
                direction="column"
                gap="1"
                className="h-full md:mt-0 mt-6"
                style={{ width: "100%" }}
              >
                {mergedBaseMenuItems.map(
                  (item: ExtendedMenuItem) => {
                    // 支持 icon 为 URL/相对路径
                    const isOpen = openSubMenus[item.path];
                    const renderIcon = (
                      icon: string,
                      labelKey: string,
                      className?: string,
                      active?: boolean,
                    ) => {
                      const link = /^(https?:\/\/|\/|\.\/|\.\.\/)/.test(icon);
                      if (link) {
                        return (
                          <InlineSvgIcon
                            src={icon}
                            alt={t(labelKey)}
                            style={{
                              width: 16,
                              height: 16,
                              objectFit: "contain",
                              opacity: active ? 1 : 0.7,
                              filter: active ? "none" : "grayscale(20%)",
                            }}
                            className={className}
                            loading="lazy"
                          />
                        );
                      }
                      const Cmp = iconMap[icon];
                      if (Cmp) {
                        return (
                          <Cmp
                            className={className}
                            style={{
                              color: active
                                ? "var(--accent-10)"
                                : "var(--gray11)",
                            }}
                          />
                        );
                      }
                      // fallback: simple dot
                      return (
                        <span
                          className={className}
                          style={{
                            width: 16,
                            height: 16,
                            display: "inline-block",
                            borderRadius: 4,
                            background: "var(--accent-8)",
                          }}
                        />
                      );
                    };
                    if (item.children && item.children.length) {
                      return (
                        <div key={item.path}>
                          <Flex
                            className="p-2 gap-2 border-l-[4px] border-transparent cursor-pointer hover:bg-accent-3 rounded-md"
                            align="center"
                            onClick={() => {
                              //const currentlyOpen = openSubMenus[item.path];
                              // 检查当前路径是否已经在该父菜单的子菜单中
                              //const isCurrentlyInThisMenu = item.children?.some(
                              //  (child) =>
                              //    location.pathname === child.path ||
                              //    location.pathname.startsWith(child.path)
                              //);

                              // 切换子菜单的展开状态
                              setOpenSubMenus((prev) => ({
                                ...prev,
                                [item.path]: !prev[item.path],
                              }));

                              //// 只有在非展开状态且不在当前菜单组中时才导航到第一个子菜单项
                              //if (
                              //  !currentlyOpen &&
                              //  !isCurrentlyInThisMenu &&
                              //  item.children &&
                              //  item.children.length > 0
                              //) {
                              //  //navigate(item.children[0].path);
                              //  // 如果是移动端，关闭侧边栏
                              //  if (isMobile) {
                              //    setSidebarOpen(false);
                              //  }
                              //}
                            }}
                          >
                            {renderIcon(
                              item.icon,
                              item.labelKey,
                              "flex w-4 h-5 items-center justify-center",
                            )}
                            <Text
                              className="text-base"
                              weight="medium"
                              style={{
                                flex: 1,
                              }}
                            >
                              {item.rawLabel || t(item.labelKey)}
                            </Text>

                            <ChevronDownIcon
                              style={{
                                transform: isOpen
                                  ? "rotate(180deg)"
                                  : "rotate(0deg)",
                                transition: "transform 0.2s",
                              }}
                            />
                          </Flex>
                          <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={
                              isOpen
                                ? { height: "auto", opacity: 1 }
                                : { height: 0, opacity: 0 }
                            }
                            transition={{ duration: 0.2 }}
                            style={{ overflow: "hidden" }}
                          >
                            <Flex direction="column" className="ml-4 gap-1">
                              {item.children.map((child: MenuItem) => (
                                <SidebarItem
                                  key={child.path}
                                  to={child.path}
                                  icon={renderIcon(
                                    child.icon,
                                    child.labelKey,
                                    "flex w-4 h-5 items-center justify-center",
                                  )}
                                  children={
                                    (child as ExtendedMenuItem).rawLabel ||
                                    t(child.labelKey)
                                  }
                                  onClick={() =>
                                    isMobile && setSidebarOpen(false)
                                  }
                                  newTab={child.newTab}
                                  reloadDocument={
                                    (child as ExtendedMenuItem).reloadDocument
                                  }
                                />
                              ))}
                            </Flex>
                          </motion.div>
                        </div>
                      );
                    }
                    const isBottomStart =
                      item.bottom && item.path === bottomStartPath;
                    return (
                      <div
                        key={item.path}
                        style={
                          isBottomStart
                            ? { marginTop: "auto" }
                            : undefined
                        }
                      >
                        <SidebarItem
                          to={item.path}
                          icon={renderIcon(
                            item.icon,
                            item.labelKey,
                            "flex w-4 h-5 items-center justify-center",
                          )}
                          children={item.rawLabel || t(item.labelKey)}
                          onClick={() => isMobile && setSidebarOpen(false)}
                          newTab={item.newTab}
                          reloadDocument={item.reloadDocument}
                        />
                      </div>
                    );
                  },
                )}
              </Flex>
            </Flex>
          </motion.div>
        </AnimatePresence>

        {/* Main Content */}
        <motion.div
          variants={contentVariants}
          animate={sidebarOpen ? "open" : "closed"}
          className="km-admin-panel-content"
          style={{
            backgroundColor: "var(--accent-3)",
            display: isMobile && sidebarOpen ? "none" : "block",
            height: "100%", // Ensure the container takes full height
            minHeight: 0,
            overflow: "hidden", // Prevent this container from scrolling
          }}
        >
          <div
            style={{
              backgroundColor: "var(--accent-1)",
              height: "100%",
              minHeight: 0,
              borderRadius: "0",
              padding: isMobile ? "8px" : "16px",
              overflowY: isConfigFormPage ? "hidden" : "auto",
              display: isConfigFormPage ? "flex" : "block",
              flexDirection: isConfigFormPage ? "column" : undefined,
              boxSizing: "border-box",
            }}
          >
            <Callout.Root mb="2" hidden={ishttps} color="red">
              <Callout.Icon>
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  width="24"
                  viewBox="0 0 24 24"
                >
                  <path
                    fill="currentColor"
                    d="M10.03 3.659c.856-1.548 3.081-1.548 3.937 0l7.746 14.001c.83 1.5-.255 3.34-1.969 3.34H4.254c-1.715 0-2.8-1.84-1.97-3.34zM12.997 17A.999.999 0 1 0 11 17a.999.999 0 0 0 1.997 0m-.259-7.853a.75.75 0 0 0-1.493.103l.004 4.501l.007.102a.75.75 0 0 0 1.493-.103l-.004-4.502z"
                  />
                </svg>
              </Callout.Icon>
              <Callout.Text>
                <Text size="2" weight="medium">
                  {t("warn_https")}
                </Text>
              </Callout.Text>
            </Callout.Root>
            {isConfigFormPage ? (
              <div className="min-h-0 flex-1">{content}</div>
            ) : (
              content
            )}
          </div>
        </motion.div>
      </Grid>
    </>
  );
};

export default AdminPanelBar;

// 侧边栏项目组件
const SidebarItem = ({
  to,
  onClick,
  icon,
  children,
  newTab,
  reloadDocument,
}: {
  to: string;
  onClick: () => void;
  icon: ReactNode;
  children: ReactNode;
  newTab?: boolean;
  reloadDocument?: boolean;
}) => {
  const location = useLocation();
  const isExternalLink = to.startsWith("http://") || to.startsWith("https://");
  // 带 query 的菜单项（如 /admin/plugin-page?short=x）做全匹配；不带 query
  // 的菜单项只比 pathname（如 /admin/plugins/config?short=x 点亮“插件配置”），
  // 同时避免前缀兄弟路由（/admin/plugins 与 /admin/plugins/config）同时点亮。
  const isActive =
    !isExternalLink &&
    to !== "/" &&
    (to.includes("?")
      ? location.pathname + location.search === to
      : location.pathname === to.split("?")[0]);
  const openInNewTab = newTab === true || (isExternalLink && newTab !== false);

  if (openInNewTab || reloadDocument) {
    return (
      <a
        href={to}
        onClick={onClick}
        target={openInNewTab ? "_blank" : undefined}
        rel={openInNewTab ? "noopener noreferrer" : undefined}
        className="group transition-colors duration-200 hover:bg-accent-3 rounded-md"
      >
        <Flex
          className="p-2 gap-2 h-full"
          align="center"
          style={{
            borderLeft: "4px solid transparent",
            borderRadius: "6px",
            backgroundColor: "transparent",
            color: "inherit",
            transition: "background-color 0.2s, border-color 0.2s",
          }}
        >
          <span
            style={{
              color: "inherit",
              opacity: 0.7,
            }}
            className="flex w-4 h-5 items-center justify-center"
          >
            {icon}
          </span>
          <Text className="text-base" weight="medium" style={{ flex: 1 }}>
            {children}
          </Text>
        </Flex>
      </a>
    );
  }

  return (
    <Link
      to={to}
      onClick={onClick}
      className="group transition-colors duration-200 hover:bg-accent-3 rounded-md"
    >
      <Flex
        className="p-2 gap-2"
        align="center"
        style={{
          borderLeft: isActive
            ? "4px solid var(--accent-8)"
            : "4px solid transparent",
          borderRadius: "6px",
          backgroundColor: isActive ? "var(--accent-4)" : "transparent",
          color: isActive ? "var(--accent-10)" : "inherit",
          transition: "background-color 0.2s, border-color 0.2s",
        }}
      >
        <span
          style={{
            color: isActive ? "var(--accent-10)" : "inherit",
            opacity: isActive ? 1 : 0.7,
          }}
          className="flex w-4 h-5 items-center justify-center"
        >
          {icon}
        </span>
        <Text
          className="text-base"
          weight={isActive ? "bold" : "medium"}
          style={{ flex: 1 }}
        >
          {children}
        </Text>
      </Flex>
    </Link>
  );
};
