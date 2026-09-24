import React from "react";
import {
  Bolt,
  Home,
  BarChart2,
  CircleArrowRight,
  MessageCircleMore,
  Ellipsis,
  Bell,
  Unplug,
  TrendingUp,
  Users,
  UserCircle,
  FileText,
  AtSign,
  Book,
  Server,
  Activity,
  Palette,
  Code,
  Globe,
  Terminal,
  Database,
  Store,
  Blocks,
  Settings2,
  LayoutDashboard,
  SquareTerminal
} from "lucide-react";


// Map icon names defined in menuConfig.json to their components
export const iconMap: Record<string, React.ComponentType<any>> = {
  Server,
  Bolt,
  Home,
  BarChart2,
  CircleArrowRight,
  MessageCircleMore,
  Ellipsis,
  Bell,
  Unplug,
  TrendingUp,
  Users,
  UserCircle,
  FileText,
  AtSign,
  Book,
  Activity,
  Palette,
  Code,
  Globe,
  Terminal,
  Database,
  Store,
  Blocks,
  Settings2,
  LayoutDashboard,
  SquareTerminal,
};
// 解析插件/插件页面声明的 icon：
// - lucide 图标名（iconMap 中存在）原样返回，由调用方用组件渲染；
// - 绝对 URL / 站内路径原样返回；
// - 其余按插件包内相对路径拼 admin 静态文件端点。
// 返回空串表示没有可用的图标（调用方用默认图标）。
export const resolvePluginIcon = (short: string, icon?: string): string => {
  if (!icon) return "";
  if (icon in iconMap) return icon;
  if (/^(https?:\/\/|\/|\.\/|\.\.\/)/.test(icon)) return icon;
  return `/api/admin/plugin/${encodeURIComponent(short)}/${icon}`;
};
