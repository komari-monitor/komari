import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Box, Button, Flex, Heading, Tabs } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import {
  SettingCard,
  SettingCardLongTextInput,
  SettingCardSelect,
  SettingCardShortTextInput,
  SettingCardSwitch,
} from "@/components/admin/SettingCard";
import SelectorDialog from "@/components/SelectorDialog";
import { useNodeList } from "@/contexts/NodeListContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import type { I18nText } from "@/utils/i18nText";

export interface ConfigFormItem {
  key?: string;
  name?: I18nText;
  help?: I18nText;
  type?: string;
  options?: string;
  default?: unknown;
  required?: boolean;
}

interface Group {
  title?: I18nText;
  items: ConfigFormItem[];
}

interface ConfigSelectionOption {
  id: string;
  name: string;
  weight?: number;
}

interface PingTaskResponse {
  id?: number;
  name?: string;
  weight?: number;
}

const MAX_SELECTED_NAMES_LENGTH = 50;

function parseSelectionValue(value: unknown): string[] {
  if (typeof value !== "string") return [];
  try {
    const source = JSON.parse(value);
    if (!Array.isArray(source)) return [];
    return source
      .filter(
        (item): item is string | number =>
          typeof item === "string" || typeof item === "number",
      )
      .map(String);
  } catch {
    return [];
  }
}

function serializeSelectionValue(type: "nodes" | "pingtasks", ids: string[]) {
  if (type === "nodes") return JSON.stringify(ids);
  return JSON.stringify(ids.map(Number));
}

function truncateSelectedNames(value: string) {
  if (value.length <= MAX_SELECTED_NAMES_LENGTH) return value;
  return `${value.slice(0, MAX_SELECTED_NAMES_LENGTH - 3)}...`;
}

function ConfigSelectionField({
  title,
  description,
  value,
  onValueChange,
  options,
  buttonLabel,
  dialogTitle,
  listLabel,
  type,
}: {
  title?: string;
  description?: string;
  value: unknown;
  onValueChange: (value: string) => void;
  options: ConfigSelectionOption[];
  buttonLabel: string;
  dialogTitle: string;
  listLabel: string;
  type: "nodes" | "pingtasks";
}) {
  const selectedIds = parseSelectionValue(value);
  const names = new Map(options.map((option) => [option.id, option.name]));
  const selectedNames = truncateSelectedNames(
    selectedIds.map((id) => names.get(id) || id).join(","),
  );
  const fieldDescription =
    description || selectedNames ? (
      <>
        {description}
        {description && selectedNames ? <br /> : null}
        {selectedNames}
      </>
    ) : undefined;

  return (
    <SettingCard title={title} description={fieldDescription}>
      <SettingCard.Action>
        <SelectorDialog
          value={selectedIds}
          onChange={(ids) => onValueChange(serializeSelectionValue(type, ids))}
          items={options}
          getId={(option) => option.id}
          getLabel={(option) => option.name}
          sortItems={(left, right) => (left.weight ?? 0) - (right.weight ?? 0)}
          title={dialogTitle}
          headerLabel={listLabel}
          className="km-config-selector-dialog"
          trigger={<Button>{buttonLabel}</Button>}
        />
      </SettingCard.Action>
    </SettingCard>
  );
}

interface ConfigFormTabsProps {
  items: ConfigFormItem[];
  values: Record<string, any>;
  onValueChange: (key: string, value: any) => void;
  resolveText: (value?: I18nText) => string | undefined;
  /** titlearea 内容（页面标题 + 保存按钮），固定不动 */
  header?: ReactNode;
  /** scrollview 顶部提示（错误/空状态等） */
  notice?: ReactNode;
  /** scrollview 左侧栏（移动端在上方），随 scrollview 一起滚动 */
  sidebar?: ReactNode;
  /** scrollview 底部内容（如底部保存按钮） */
  footer?: ReactNode;
  className?: string;
  formClassName?: string;
}

const SPY_LINE_OFFSET = 16;

/**
 * main > titlearea + scrollview > configarea 结构：
 * - titlearea 固定不滚动，含 header 和分类 Tab（横向可滚动）
 * - scrollview 是独立滚动窗口，内部渲染 notice/sidebar/分组配置项/footer
 * - 滚动时自动高亮当前分组对应的 Tab
 */
const ConfigFormTabs = ({
  items,
  values,
  onValueChange,
  resolveText,
  header,
  notice,
  sidebar,
  footer,
  className,
  formClassName,
}: ConfigFormTabsProps) => {
  const { t } = useTranslation();
  const { nodeList } = useNodeList();
  const { call } = useRPC2Call();
  const [activeTab, setActiveTab] = useState(0);
  const [pingTasks, setPingTasks] = useState<PingTaskResponse[]>([]);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const sectionRefs = useRef<(HTMLDivElement | null)[]>([]);
  const lastTabRef = useRef(0);
  const rafRef = useRef<number | null>(null);
  const scrollEndTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const suppressSpyRef = useRef(false);

  const groups = useMemo(() => {
    const result: Group[] = [];
    let current: Group | null = null;
    let pendingTextboxes: ConfigFormItem[] = [];
    for (const item of items) {
      if (item.type === "title") {
        current = { title: item.name, items: pendingTextboxes };
        pendingTextboxes = [];
        result.push(current);
      } else if (item.type === "textbox" && !current) {
        pendingTextboxes.push(item);
      } else if (item.key || item.type === "textbox") {
        if (!current) {
          current = { title: undefined, items: pendingTextboxes };
          pendingTextboxes = [];
          result.push(current);
        }
        current.items.push(item);
      }
    }
    if (pendingTextboxes.length > 0) {
      if (result.length > 0) {
        result[0].items.unshift(...pendingTextboxes);
      } else {
        result.push({ title: undefined, items: pendingTextboxes });
      }
    }
    return result;
  }, [items]);

  const nodeOptions = useMemo<ConfigSelectionOption[]>(
    () =>
      (nodeList ?? []).map((node) => ({
        id: node.uuid,
        name: node.name,
        weight: node.weight,
      })),
    [nodeList],
  );
  const pingTaskOptions = useMemo<ConfigSelectionOption[]>(
    () =>
      pingTasks
        .filter((task) => task.id !== undefined)
        .map((task) => ({
          id: String(task.id),
          name: task.name || String(task.id),
          weight: task.weight,
        })),
    [pingTasks],
  );
  const hasPingTaskFields = items.some((item) => item.type === "pingtasks");

  useEffect(() => {
    if (!hasPingTaskFields) {
      setPingTasks([]);
      return;
    }
    void call<any, PingTaskResponse[]>("admin:getAllPingTasks")
      .then((result) => setPingTasks(Array.isArray(result) ? result : []))
      .catch(() => setPingTasks([]));
  }, [call, hasPingTaskFields]);

  useEffect(() => {
    setActiveTab(0);
    lastTabRef.current = 0;
  }, [groups]);

  useEffect(
    () => () => {
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
      if (scrollEndTimerRef.current !== null)
        clearTimeout(scrollEndTimerRef.current);
    },
    [],
  );

  const currentTab = Math.min(activeTab, Math.max(groups.length - 1, 0));
  const hasTabs = groups.length > 1;

  const updateActiveFromScroll = useCallback(() => {
    const container = scrollRef.current;
    if (!container || groups.length === 0) return;
    const line = container.getBoundingClientRect().top + SPY_LINE_OFFSET;
    const sections = sectionRefs.current;
    let current = 0;
    for (let i = 0; i < sections.length; i++) {
      const el = sections[i];
      if (el && el.getBoundingClientRect().top <= line) current = i;
    }
    if (
      container.scrollTop + container.clientHeight >=
      container.scrollHeight - 2
    ) {
      current = sections.length - 1;
    }
    if (current !== lastTabRef.current) {
      lastTabRef.current = current;
      setActiveTab(current);
    }
  }, [groups.length]);

  const resumeSpy = useCallback(() => {
    suppressSpyRef.current = false;
    updateActiveFromScroll();
  }, [updateActiveFromScroll]);

  const clearScrollEndTimer = () => {
    if (scrollEndTimerRef.current !== null) {
      clearTimeout(scrollEndTimerRef.current);
      scrollEndTimerRef.current = null;
    }
  };

  const handleScroll = useCallback(() => {
    if (suppressSpyRef.current) {
      clearScrollEndTimer();
      scrollEndTimerRef.current = setTimeout(resumeSpy, 120);
      return;
    }
    if (rafRef.current !== null) return;
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = null;
      updateActiveFromScroll();
    });
  }, [resumeSpy, updateActiveFromScroll]);

  const handleTabChange = (value: string) => {
    const index = Number(value);
    if (Number.isNaN(index)) return;
    suppressSpyRef.current = true;
    setActiveTab(index);
    lastTabRef.current = index;
    const el = sectionRefs.current[index];
    const container = scrollRef.current;
    if (el && container) {
      const containerRect = container.getBoundingClientRect();
      const elRect = el.getBoundingClientRect();
      container.scrollTo({
        top: container.scrollTop + (elRect.top - containerRect.top),
        behavior: "smooth",
      });
    }
    clearScrollEndTimer();
    scrollEndTimerRef.current = setTimeout(resumeSpy, 400);
  };

  const renderField = (item: ConfigFormItem, index: number) => {
    const title = resolveText(item.name);
    const description = resolveText(item.help);
    if (item.type === "textbox") {
      return (
        <Box
          key={`textbox-${index}`}
          className="km-config-textbox"
          dangerouslySetInnerHTML={{ __html: title || "" }}
        />
      );
    }

    const key = item.key!;
    const value = values[key];
    switch (item.type) {
      case "nodes":
        return (
          <Box key={key} id={key}>
            <ConfigSelectionField
              title={title}
              description={description}
              value={value}
              onValueChange={(nextValue) => onValueChange(key, nextValue)}
              options={nodeOptions}
              buttonLabel={t("common.select_nodes")}
              dialogTitle={title || t("common.select_nodes")}
              listLabel={t("common.server")}
              type="nodes"
            />
          </Box>
        );
      case "pingtasks":
        return (
          <Box key={key} id={key}>
            <ConfigSelectionField
              title={title}
              description={description}
              value={value}
              onValueChange={(nextValue) => onValueChange(key, nextValue)}
              options={pingTaskOptions}
              buttonLabel={t("common.select_ping_tasks")}
              dialogTitle={title || t("common.select_ping_tasks")}
              listLabel={t("common.name")}
              type="pingtasks"
            />
          </Box>
        );
      case "switch":
        return (
          <Box key={key} id={key}>
            <SettingCardSwitch
              title={title}
              description={description}
              defaultChecked={!!value}
              onChange={(checked) => onValueChange(key, checked)}
            />
          </Box>
        );
      case "select": {
        const options = (item.options || "")
          .split(",")
          .map((o) => o.trim())
          .filter(Boolean)
          .map((o) => ({ value: o }));
        return (
          <Box key={key} id={key}>
            <SettingCardSelect
              title={title}
              description={description}
              value={value}
              options={options}
              OnSave={(v) => onValueChange(key, v)}
              label={value !== undefined ? String(value) : t("common.select")}
            />
          </Box>
        );
      }
      case "number":
        return (
          <Box key={key} id={key}>
            <SettingCardShortTextInput
              title={title}
              description={description}
              type="number"
              showSaveButton={false}
              value={value !== undefined ? String(value) : ""}
              onChange={(e) =>
                onValueChange(
                  key,
                  e.target.value === "" ? undefined : Number(e.target.value),
                )
              }
            />
          </Box>
        );
      case "richtext":
        return (
          <Box key={key} id={key}>
            <SettingCardLongTextInput
              title={title}
              description={description}
              defaultValue={value !== undefined ? String(value) : ""}
              showSaveButton={false}
              onChange={(e) => onValueChange(key, e.target.value)}
            />
          </Box>
        );
      case "string":
      default:
        return (
          <Box key={key} id={key}>
            <SettingCardShortTextInput
              title={title}
              description={description}
              value={value !== undefined ? String(value) : ""}
              required={item.required}
              showSaveButton={false}
              onChange={(e) => onValueChange(key, e.target.value)}
            />
          </Box>
        );
    }
  };

  const renderSections = () =>
    groups.map((group, index) => (
      <Box
        key={index}
        ref={(el) => {
          sectionRefs.current[index] = el;
        }}
      >
        {group.title && (
          <Heading size="3">
            {resolveText(group.title) || t("common.title")}
          </Heading>
        )}
        <Flex direction="column" gap="3" className="mt-5 mb-3">
          {group.items.map(renderField)}
        </Flex>
      </Box>
    ));

  return (
    <Flex direction="column" className={`h-full min-h-0 ${className}`}>
      {/* titlearea：固定顶部，不随滚动移动 */}
      <Box className="shrink-0 mb-5">
        <Flex direction="column" gap="3">
          {header}
          {hasTabs && (
            <Tabs.Root
              value={String(currentTab)}
              onValueChange={handleTabChange}
            >
              <Tabs.List className="km-config-form-tabs-list overflow-x-auto">
                {groups.map((group, index) => (
                  <Tabs.Trigger
                    key={index}
                    value={String(index)}
                    className="shrink-0 whitespace-nowrap"
                  >
                    {group.title
                      ? resolveText(group.title) || t("common.title")
                      : t("settings.general.title")}
                  </Tabs.Trigger>
                ))}
              </Tabs.List>
            </Tabs.Root>
          )}
        </Flex>
      </Box>

      {/* scrollview：独立滚动窗口，内容（configarea）在其中滚动 */}
      <Box
        ref={scrollRef}
        onScroll={handleScroll}
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain"
      >
        {notice}
        {sidebar ? (
          <Flex
            direction={{ initial: "column", md: "row" }}
            gap="4"
            align="start"
          >
            <Box className="w-full shrink-0 md:w-64">{sidebar}</Box>
            <Flex
              direction="column"
              gap="3"
              className={`min-w-0 flex-1 ${formClassName}`}
            >
              {renderSections()}
            </Flex>
          </Flex>
        ) : (
          <Flex direction="column" gap="3" className={formClassName}>
            {renderSections()}
          </Flex>
        )}
        {footer}
      </Box>
    </Flex>
  );
};

export default ConfigFormTabs;
