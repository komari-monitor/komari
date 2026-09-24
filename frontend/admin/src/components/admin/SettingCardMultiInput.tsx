import React from "react";
import { Button, Flex, TextField, TextArea } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { SettingCardCollapse } from "./SettingCard";

interface InputItem {
  tag: string;
  label: string;
  type?: "short" | "long";
  placeholder?: string;
  defaultValue?: string;
  disabled?: boolean;
  number?: boolean;
}

type OnSaveHandler = (values: Record<string, string>) => Promise<any> | any;

export function SettingCardMultiInputCollapse({
  title = "",
  description = "",
  defaultOpen = false,
  items,
  onSave,
  isSaving,
  children,
  onChange, // 新增 onChange
}: {
  title?: string;
  description?: string;
  defaultOpen?: boolean;
  items: InputItem[];
  onSave: OnSaveHandler;
  /** 外部控制的保存状态，如果提供则覆盖内部状态 */
  isSaving?: boolean;
  /** 在保存按钮之前渲染的额外节点 */
  children?: React.ReactNode;
  /** 输入值变化时的回调 */
  onChange?: (values: Record<string, string>) => void;
}) {
  const { t } = useTranslation();
  const [values, setValues] = React.useState<Record<string, string>>(
    () =>
      items.reduce((acc, item) => {
        acc[item.tag] = item.defaultValue || "";
        return acc;
      }, {} as Record<string, string>)
  );
  const [saving, setSaving] = React.useState(false);
  const savingState = isSaving !== undefined ? isSaving : saving;

  const handleChange = (tag: string, value: string) => {
    setValues((prev) => {
      const newValues = { ...prev, [tag]: value };
      if (onChange) onChange(newValues);
      return newValues;
    });
  };

  const handleSave = async () => {
    if (isSaving === undefined) setSaving(true);
    try {
      await onSave(values);
    } finally {
      if (isSaving === undefined) setSaving(false);
    }
  };

  return (
    <SettingCardCollapse title={title} description={description} defaultOpen={defaultOpen}>
      {/* 渲染 Header slot */}
      {React.Children.map(children, (child) => {
        if (
          React.isValidElement(child) &&
          child.type === SettingCardCollapse.Header
        ) {
          return child;
        }
        return null;
      })}
      <Flex direction="column" gap="2" className="km-setting-card-multi-input w-full">
        {items.map((item) => (
          <React.Fragment key={item.tag}>
            <label className="text-sm font-semibold">{item.label}</label>
            {item.type === "long" ? (
              <TextArea
                className="w-full"
                defaultValue={item.defaultValue}
                value={values[item.tag]}
                placeholder={item.placeholder}
                disabled={item.disabled}
                onChange={(e) => handleChange(item.tag, e.target.value)}
                resize="vertical"
                ref={undefined}
              />
            ) : (
              <TextField.Root
                className="w-full"
                defaultValue={item.defaultValue}
                value={values[item.tag]}
                placeholder={item.placeholder}
                disabled={item.disabled}
                type={item.number ? "number" : "text"}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                  handleChange(item.tag, e.target.value)
                }
                ref={undefined}
              />
            )}
          </React.Fragment>
        ))}
        {/* 渲染除 Header 外的 children */}
        {React.Children.map(children, (child) => {
          if (
            React.isValidElement(child) &&
            child.type === SettingCardCollapse.Header
          ) {
            return null;
          }
          return child;
        })}
        <div>
          <Button variant="solid" className="mt-2" onClick={handleSave} disabled={savingState}>
            {t("common.save")}
          </Button>
        </div>
      </Flex>
    </SettingCardCollapse>
  );
}
