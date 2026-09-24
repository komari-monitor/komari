import {
    SettingCardCollapse,
    SettingCardSelect,
    SettingCardSwitch,
    SettingCardShortTextInput,
    SettingCardLongTextInput,
    SettingCardButton,
} from "@/components/admin/SettingCard";
import type React from "react";
import { toast } from "sonner";

interface RenderProviderInputsProps {
    currentProvider: string;
    providerDefs: any;
    providerValues: any;
    translationPrefix?: string;
    title?: string;
    description?: string;
    footer?: React.ReactNode | string;
    setProviderValues: (updater: (prev: any) => any) => void;
    handleSave: (values: any) => Promise<void>;
    t: any;
}

export const renderProviderInputs = ({
    currentProvider,
    providerDefs,
    providerValues,
    translationPrefix,
    title,
    description,
    footer,
    setProviderValues,
    handleSave,
    t,
}: RenderProviderInputsProps) => {

    if (!currentProvider || !providerDefs[currentProvider]) return null;

    const fields = providerDefs[currentProvider];

    // 统一保存所有字段
    const handleSaveAll = async () => {
        try {
            // 直接使用 providerValues
            const finalValues = { ...providerValues };

            // 验证必填字段
            const requiredFields = fields.filter((f: any) => f.required);
            const missingFields = requiredFields.filter((f: any) => {
                const value = finalValues[f.name];
                return value === undefined || value === null || (typeof value === 'string' && value.trim() === "");
            });

            if (missingFields.length > 0) {
                const fieldNames = missingFields.map((f: any) => t(`${translationPrefix}.${f.name}`, f.name)).join(", ");
                toast.error(
                    t("settings.missing_required_fields", { fieldNames })
                );
                return;
            }

            // 转换数字类型字段
            const processedValues = { ...finalValues };
            fields.forEach((f: any) => {
                const value = processedValues[f.name];

                // 跳过未定义或null的值
                if (value === undefined || value === null) {
                    return;
                }

                if (["int", "int64"].includes(f.type)) {
                    const numValue = value === "" ? 0 : Number(value);
                    if (isNaN(numValue)) {
                        toast.error(t("settings.invalid_number", { field: t(`${translationPrefix}.${f.name}`, f.name) }));
                        throw new Error(`Invalid integer value for ${f.name}`);
                    }
                    if (!Number.isInteger(numValue)) {
                        toast.error(t("settings.invalid_integer", { field: t(`${translationPrefix}.${f.name}`, f.name) }));
                        throw new Error(`Value must be an integer for ${f.name}`);
                    }
                    processedValues[f.name] = numValue;
                } else if (["float32", "float64"].includes(f.type)) {
                    const numValue = value === "" ? 0 : Number(value);
                    if (isNaN(numValue)) {
                        toast.error(t("settings.invalid_number", { field: t(`${translationPrefix}.${f.name}`, f.name) }));
                        throw new Error(`Invalid float value for ${f.name}`);
                    }
                    processedValues[f.name] = numValue;
                }
            });

            await handleSave(processedValues);
        } catch (error) {
            console.error("Validation error:", error);
            throw error;
        }
    };

    // 更新本地值
    const updateLocalValue = (fieldName: string, value: any) => {
        setProviderValues((v: any) => ({ ...v, [fieldName]: value }));
    };

    // 渲染单个字段
    const renderField = (f: any) => {
        const fieldTitle = String(t(`${translationPrefix}.${f.name}`, f.name)) + (f.required ? " *" : "");
        const fieldDescription = f.help ? String(t(`${translationPrefix}.${f.name}_help`, f.help)) : undefined;
        const fieldValue = providerValues[f.name] !== undefined ? providerValues[f.name] : (f.default || "");

        // 选择框类型
        if (f.type === "option" && f.options) {
            return (
                <SettingCardSelect
                    key={f.name}
                    bordless
                    title={fieldTitle}
                    description={fieldDescription}
                    options={f.options.split(",").map((opt: string) => ({ value: opt, label: opt }))}
                    defaultValue={fieldValue}
                    OnSave={(val: string) => {
                        updateLocalValue(f.name, val);
                    }}
                />
            );
        }

        // 开关类型
        if (f.type === "bool") {
            return (
                <SettingCardSwitch
                    bordless
                    key={f.name}
                    title={fieldTitle}
                    description={fieldDescription}
                    defaultChecked={fieldValue !== undefined ? !!fieldValue : (f.default === "true" || f.default === true)}
                    onChange={(checked: boolean) => {
                        updateLocalValue(f.name, checked);
                    }}
                />
            );
        }

        // 长文本类型 (richtext)
        if (f.type === "richtext" || f.type === "text") {
            return (
                <SettingCardLongTextInput
                    key={f.name}
                    bordless
                    title={fieldTitle}
                    description={fieldDescription}
                    defaultValue={String(fieldValue)}
                    showSaveButton={false}
                    onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => {
                        updateLocalValue(f.name, e.target.value);
                    }}
                    OnSave={(value: string) => {
                        updateLocalValue(f.name, value);
                    }}
                />
            );
        }

        // 短文本和数字类型
        const isNumber = ["int", "int64", "float32", "float64"].includes(f.type);
        return (
            <SettingCardShortTextInput
                key={f.name}
                bordless
                title={fieldTitle}
                description={fieldDescription}
                defaultValue={String(fieldValue)}
                type={isNumber ? "number" : "text"}
                showSaveButton={false}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                    const value = isNumber ? (e.target.value === "" ? 0 : Number(e.target.value)) : e.target.value;
                    updateLocalValue(f.name, value);
                }}
                OnSave={() => {
                    // 这里不做任何操作，只是为了满足接口要求
                }}
            />
        );
    };

    return (
        <div key={currentProvider}>
            <SettingCardCollapse
                title={title ?? t("common.details")}
                description={description}
                defaultOpen={true}
            >
                <div className="flex gap-4 flex-col">
                    {/* 按照服务器传来的顺序渲染所有字段 */}
                    {fields.map((f: any) => renderField(f))}

                    {/* 底部说明 */}
                    {footer && (
                        <label className="text-sm text-muted-foreground mt-2 block">
                            {footer}
                        </label>
                    )}

                    {/* 统一的保存按钮 */}
                    <SettingCardButton
                        bordless
                        onClick={handleSaveAll}
                    >
                        {t("common.save")}
                    </SettingCardButton>
                </div>
            </SettingCardCollapse>
        </div>
    );
};