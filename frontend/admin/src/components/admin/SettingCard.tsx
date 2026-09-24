import {
  Button,
  DropdownMenu,
  Flex,
  IconButton,
  Switch,
  TextArea,
  TextField,
} from "@radix-ui/themes";
import React from "react";
import { useTranslation } from "react-i18next";
import { ChevronDownIcon } from "lucide-react";
import { AnimatePresence, motion } from "framer-motion"; // 引入 Framer Motion

interface SettingCardProps {
  title?: string | React.ReactNode;
  description?: string | React.ReactNode;
  children?: React.ReactNode;
  className?: string;
  bordless?: boolean;
  direction?: "row" | "column" | "row-reverse" | "column-reverse";
  onHeaderClick?: () => void;
}

export function SettingCard({
  title = "",
  description = "",
  children,
  className = "",
  direction = "column",
  bordless = false,
  onHeaderClick = () => { },
}: SettingCardProps) {
  const actionChild = React.Children.toArray(children).find(
    (child) => React.isValidElement(child) && child.type === Action
  );

  const otherChildren = React.Children.toArray(children).filter(
    (child) => !(React.isValidElement(child) && child.type === Action)
  );

  return (
    <Flex
      direction={direction}
      justify="between"
      align="center"
      wrap="wrap"
      style={{ borderColor: "var(--gray-a5)" }}
      className={
        bordless
          ? "km-setting-card border-0"
          : "km-setting-card border-1 rounded-md py-2 px-4 bg-transparent  min-h-8 " + className
      }
    >
      <Flex
        className="w-full"
        direction="row"
        justify="between"
        align="center"
        wrap="nowrap"
        onClick={onHeaderClick}
      >
        <Flex
          direction="column"
          gap="1"
          className="min-h-10"
          justify={"center"}
        >
          <label className="text-base font-medium" style={{ fontWeight: 600 }}>
            {title}
          </label>
          {description && (
            <label className="text-sm text-muted-foreground">
              {description}
            </label>
          )}
        </Flex>
        {actionChild}
      </Flex>
      {otherChildren}
    </Flex>
  );
}

function Action({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}

SettingCard.Action = Action;

export function SettingCardSwitch({
  label = "",
  autoDisabled = true,
  defaultChecked,
  onChange,
  ...props
}: SettingCardProps & {
  label?: string;
  autoDisabled?: boolean;
  defaultChecked?: boolean;
  onChange?: (checked: boolean, switchElement: HTMLButtonElement) => void;
}) {
  const switchRef = React.useRef<HTMLButtonElement>(null);
  const [disabled, setDisabled] = React.useState(false);
  const [checked, setChecked] = React.useState(defaultChecked || false);

  React.useEffect(() => {
    setChecked(Boolean(defaultChecked));
  }, [defaultChecked]);

  const handleChange = (c: boolean) => {
    if (autoDisabled) setDisabled(true);
    const previousValue = checked;
    setChecked(c);
    const result: any =
      onChange && switchRef.current
        ? onChange(c, switchRef.current)
        : undefined;
    if (autoDisabled) {
      const promise: Promise<any> = result;
      if (promise && typeof promise.then === "function") {
        promise
          .then(() => { })
          .catch(() => {
            setChecked(previousValue);
          })
          .finally(() => {
            setDisabled(false);
          });
      } else {
        setDisabled(false);
      }
    }
  };
  return (
    <SettingCard {...props} direction="column">
      <SettingCard.Action>
        <Flex direction="row" gap="2" align="center">
          <label>{label}</label>
          <Switch
            ref={switchRef}
            checked={checked}
            onCheckedChange={handleChange}
            disabled={disabled}
          />
        </Flex>
      </SettingCard.Action>
    </SettingCard>
  );
}

export function SettingCardButton({
  label = "",
  variant = "solid",
  children,
  onClick,
  autoDisabled = true,
  ...props
}: SettingCardProps & {
  label?: string;
  variant?: "solid" | "soft" | "outline" | "ghost";
  children?: React.ReactNode;
  onClick?: (buttonElement: HTMLButtonElement) => void;
  autoDisabled?: boolean;
}) {
  const [disabled, setDisabled] = React.useState(false);
  const resolvedLabel = label;
  const handleClick = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (autoDisabled) setDisabled(true);
    const result: any = onClick ? onClick(event.currentTarget) : undefined;
    if (autoDisabled) {
      const promise: Promise<any> = result;
      if (promise && typeof promise.then === "function") {
        promise.finally(() => setDisabled(false)).catch(() => {});
      } else {
        setDisabled(false);
      }
    }
  };
  return (
    <SettingCard {...props} direction="column">
      <SettingCard.Action>
        <Flex>
          <Flex direction="row" gap="2" align="center">
            <label>{resolvedLabel}</label>
            <Button onClick={handleClick} variant={variant} disabled={disabled}>
              {children}
            </Button>
          </Flex>
        </Flex>
      </SettingCard.Action>
    </SettingCard>
  );
}

export function SettingCardIconButton({
  label = "",
  variant = "solid",
  children,
  onClick,
  autoDisabled = true,
  ...props
}: SettingCardProps & {
  label?: string;
  variant?: "solid" | "soft" | "outline" | "ghost";
  children?: React.ReactNode;
  onClick?: (buttonElement: HTMLButtonElement) => void;
  autoDisabled?: boolean;
}) {
  const [disabled, setDisabled] = React.useState(false);
  const resolvedLabel = label;
  const handleClick = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (autoDisabled) setDisabled(true);
    const result: any = onClick ? onClick(event.currentTarget) : undefined;
    if (autoDisabled) {
      const promise: Promise<any> = result;
      if (promise && typeof promise.then === "function") {
        promise.finally(() => setDisabled(false)).catch(() => {});
      } else {
        setDisabled(false);
      }
    }
  };
  return (
    <SettingCard {...props} direction="column">
      <SettingCard.Action>
        <Flex>
          <Flex direction="row" gap="2" align="center">
            <label>{resolvedLabel}</label>
            <IconButton
              onClick={handleClick}
              variant={variant}
              disabled={disabled}
              aria-label={resolvedLabel || undefined}
            >
              {children}
            </IconButton>
          </Flex>
        </Flex>
      </SettingCard.Action>
    </SettingCard>
  );
}

interface SettingCardShortTextInputProps
  extends Omit<React.ComponentProps<typeof TextField.Root>, 'onChange' | 'onKeyDown'> {
  // SettingCard 相关属性
  title?: string;
  description?: string;
  descriptionPlacement?: "header" | "footer";
  bordless?: boolean;

  // 按钮相关属性
  showSaveButton?: boolean;
  label?: string;
  autoDisabled?: boolean;
  isSaving?: boolean;

  // 保存回调
  OnSave?: (
    value: string,
    inputElement: HTMLInputElement,
    buttonElement: HTMLButtonElement
  ) => void | Promise<unknown>;

  // 额外内容
  children?: React.ReactNode | null;

  // 输入框事件回调 (可选，用于额外处理)
  onChange?: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onKeyDown?: (e: React.KeyboardEvent<HTMLInputElement>) => void;
}

export function SettingCardShortTextInput({
  // SettingCard 属性
  title = "",
  description = "",
  descriptionPlacement = "header",
  bordless = false,

  // 按钮属性
  showSaveButton = true,
  label = "",
  autoDisabled = true,
  isSaving,

  // 保存回调
  OnSave = () => { },

  // 额外内容
  children = null,

  // 事件回调
  onChange,
  onKeyDown,

  // TextField.Root 的所有其他属性
  value,
  defaultValue,
  placeholder,
  disabled,
  type = "text",
  required,
  readOnly,
  maxLength,
  minLength,
  pattern,
  autoComplete,
  autoFocus,
  name,
  id,
  className = "w-full",
  ...restProps
}: SettingCardShortTextInputProps) {
  const { t } = useTranslation();
  const [internalDisabled, setInternalDisabled] = React.useState(false);
  const savingState = Boolean(isSaving) || internalDisabled;
  const normalizedValue =
    value !== undefined && value !== null ? String(value) : "";
  const normalizedDefaultValue =
    defaultValue !== undefined && defaultValue !== null
      ? String(defaultValue)
      : "";
  const [internalValue, setInternalValue] = React.useState(
    value !== undefined ? normalizedValue : normalizedDefaultValue
  );
  const previousDefaultValueRef = React.useRef(normalizedDefaultValue);
  const currentValue = value !== undefined ? normalizedValue : internalValue;
  const inputRef = React.useRef<HTMLInputElement>(null);
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const resolvedLabel = label || t("common.save");

  // 当外部value改变时，同步内部状态
  React.useEffect(() => {
    if (value !== undefined) {
      setInternalValue(normalizedValue);
      previousDefaultValueRef.current = normalizedDefaultValue;
      return;
    }

    if (normalizedDefaultValue !== previousDefaultValueRef.current) {
      const previousDefaultValue = previousDefaultValueRef.current;
      setInternalValue((currentInternalValue) =>
        currentInternalValue === previousDefaultValue
          ? normalizedDefaultValue
          : currentInternalValue
      );
      previousDefaultValueRef.current = normalizedDefaultValue;
    }
  }, [normalizedDefaultValue, normalizedValue, value]);

  const handleSave = () => {
    if (autoDisabled) setInternalDisabled(true);
    const valueToSave = currentValue?.toString() || "";
    const result: any =
      inputRef.current && buttonRef.current
        ? OnSave(valueToSave, inputRef.current, buttonRef.current)
        : undefined;
    if (autoDisabled) {
      const promise: Promise<any> = result;
      if (promise && typeof promise.then === "function") {
        promise
          .finally(() => setInternalDisabled(false))
          .catch(() => {});
      } else {
        setInternalDisabled(false);
      }
    }
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;

    // 只有在非受控模式下才更新内部状态
    if (value === undefined) {
      setInternalValue(newValue);
    }

    // 调用外部传入的 onChange 回调
    onChange?.(e);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    // 按 Enter 键时触发保存
    if (e.key === 'Enter') {
      e.preventDefault();
      handleSave();
    }

    // 调用外部传入的 onKeyDown 回调
    onKeyDown?.(e);
  };

  const showFooterDescription =
    descriptionPlacement === "footer" &&
    description !== undefined &&
    description !== null &&
    description !== "";
  const showFooterRow =
    descriptionPlacement === "footer" &&
    (showFooterDescription || showSaveButton);
  const showHiddenFooterSaveButton =
    descriptionPlacement === "footer" &&
    !showFooterDescription &&
    !showSaveButton;

  return (
    <SettingCard
      title={title}
      description={descriptionPlacement === "footer" ? undefined : description}
      bordless={bordless}
    >
      <Flex direction="column" className="w-full mt-1" gap="2" align="start">
        <TextField.Root
          {...restProps}
          className={className}
          value={currentValue}
          defaultValue={value === undefined ? normalizedDefaultValue : undefined}
          placeholder={placeholder}
          disabled={disabled || savingState}
          type={type}
          required={required}
          readOnly={readOnly}
          maxLength={maxLength}
          minLength={minLength}
          pattern={pattern}
          autoComplete={autoComplete}
          autoFocus={autoFocus}
          name={name}
          id={id}
          onChange={handleInputChange}
          onKeyDown={handleKeyDown}
          ref={inputRef}
        >
        </TextField.Root>
        {children}
        {descriptionPlacement === "footer" ? (
          showFooterRow ? (
            <Flex
              direction="row"
              className="w-full"
              align="center"
              justify={showFooterDescription ? "between" : "end"}
              gap="3"
            >
              {showFooterDescription ? (
                <label className="min-w-0 flex-1 text-sm text-muted-foreground">
                  {description}
                </label>
              ) : null}
              <Button
                ref={buttonRef}
                onClick={handleSave}
                variant="solid"
                hidden={!showSaveButton}
                disabled={savingState}
              >
                {resolvedLabel}
              </Button>
            </Flex>
          ) : showHiddenFooterSaveButton ? (
            <Button
              ref={buttonRef}
              onClick={handleSave}
              variant="solid"
              hidden
              disabled={savingState}
            >
              {resolvedLabel}
            </Button>
          ) : null
        ) : (
          <Button
            ref={buttonRef}
            onClick={handleSave}
            variant="solid"
            hidden={!showSaveButton}
            disabled={savingState}
          >
            {resolvedLabel}
          </Button>
        )}
      </Flex>
    </SettingCard>
  );
}

export function SettingCardLongTextInput({
  title = "",
  description = "",
  descriptionPlacement = "header",
  label = "",
  defaultValue = "",
  OnSave = () => { },
  onChange,
  autoDisabled = true,
  isSaving,
  bordless = false,
  showSaveButton = true,
}: {
  title?: string;
  description?: string;
  descriptionPlacement?: "header" | "footer";
  label?: string;
  defaultValue?: string;
  OnSave?: (
    value: string,
    textAreaElement: HTMLTextAreaElement,
    buttonElement: HTMLButtonElement
  ) => void | Promise<unknown>;
  onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
  autoDisabled?: boolean;
  isSaving?: boolean;
  bordless?: boolean;
  showSaveButton?: boolean;
}) {
  const { t } = useTranslation();
  const [disabled, setDisabled] = React.useState(false);
  const savingState = Boolean(isSaving) || disabled;
  const [value, setValue] = React.useState(defaultValue);
  const textAreaRef = React.useRef<HTMLTextAreaElement>(null);
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const resolvedLabel = label || t("common.save");

  React.useEffect(() => {
    setValue(defaultValue);
  }, [defaultValue]);

  const handleSave = () => {
    if (autoDisabled) setDisabled(true);
    const result: any =
      textAreaRef.current && buttonRef.current
        ? OnSave(value, textAreaRef.current, buttonRef.current)
        : undefined;
    if (autoDisabled) {
      const promise: Promise<any> = result;
      if (promise && typeof promise.then === "function") {
        promise.finally(() => setDisabled(false)).catch(() => {});
      } else {
        setDisabled(false);
      }
    }
  };

  const handleTextAreaChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setValue(e.target.value);
    // 调用外部传入的 onChange 回调
    onChange?.(e);
  };

  const showFooterDescription =
    descriptionPlacement === "footer" &&
    description !== undefined &&
    description !== null &&
    description !== "";

  return (
    <SettingCard
      title={title}
      description={descriptionPlacement === "footer" ? undefined : description}
      bordless={bordless}
    >
      <Flex direction="column" className="w-full mt-1" gap="2" align="start">
        <TextArea
          className="w-full"
          defaultValue={defaultValue}
          resize="vertical"
          value={value}
          onChange={handleTextAreaChange}
          ref={textAreaRef}
        />
        {descriptionPlacement === "footer" ? (
          showFooterDescription || showSaveButton ? (
            <Flex
              direction="row"
              className="w-full"
              align="center"
              justify={showFooterDescription ? "between" : "end"}
              gap="3"
            >
              {showFooterDescription ? (
                <label className="min-w-0 flex-1 text-sm text-muted-foreground">
                  {description}
                </label>
              ) : null}
              {showSaveButton ? (
                <Button
                  ref={buttonRef}
                  onClick={handleSave}
                  variant="solid"
                  disabled={savingState}
                >
                  {resolvedLabel}
                </Button>
              ) : null}
            </Flex>
          ) : null
        ) : showSaveButton ? (
          <Button
            ref={buttonRef}
            onClick={handleSave}
            variant="solid"
            disabled={savingState}
          >
            {resolvedLabel}
          </Button>
        ) : null}
      </Flex>
    </SettingCard>
  );
}

export function SettingCardSelect({
  title,
  description,
  defaultValue = "",
  value,
  label = "",
  options = [],
  OnSave = () => { },
  autoDisabled = true,
  isSaving,
  bordless = false,
}: {
  title?: string;
  description?: string;
  defaultValue?: string;
  value?: string;
  label?: string;
  options?: { value: string; label?: string; disabled?: boolean }[];
  OnSave?: (value: string, buttonElement: HTMLButtonElement) => void;
  autoDisabled?: boolean;
  isSaving?: boolean;
  bordless?: boolean;
}) {
  const { t } = useTranslation();
  const [disabled, setDisabled] = React.useState(false);
  const savingState = isSaving !== undefined ? isSaving : disabled;
  const [selectedValue, setSelectedValue] = React.useState(
    value !== undefined ? value : defaultValue
  );
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const resolvedLabel = label || t("common.select");

  React.useEffect(() => {
    if (value !== undefined) {
      setSelectedValue(value);
    }
  }, [value]);

  const handleSave = (value: string) => {
    if (isSaving === undefined && autoDisabled) setDisabled(true);
    const previousValue = selectedValue; // 保存之前的值
    setSelectedValue(value); // 先更新选择的值

    const result: any = buttonRef.current
      ? OnSave(value, buttonRef.current)
      : undefined;
    if (autoDisabled) {
      const promise: Promise<any> = result;
      if (promise && typeof promise.then === "function") {
        promise
          .then(() => {
            // 成功时不需要额外操作，值已经更新
          })
          .catch(() => {
            // 错误时自动切换回之前的值
            setSelectedValue(previousValue);
          })
          .finally(() => {
            if (isSaving === undefined) {
              setDisabled(false);
            }
          });
      } else {
        if (isSaving === undefined) {
          setDisabled(false);
        }
      }
    }
  };

  // 获取要显示的文本，优先显示选择的值对应的标签
  const getDisplayText = () => {
    if (selectedValue) {
      const selectedOption = options.find(
        (option) => option.value === selectedValue
      );
      return selectedOption?.label || selectedValue;
    }
    return resolvedLabel;
  };

  return (
    <SettingCard title={title} description={description} bordless={bordless}>
      <SettingCard.Action>
        <Flex>
          <Flex direction="row" gap="2" align="center">
            <DropdownMenu.Root>
              <DropdownMenu.Trigger disabled={savingState}>
                <Button variant="soft" ref={buttonRef}>
                  {getDisplayText()}
                  <DropdownMenu.TriggerIcon />
                </Button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Content>
                {options.map((option) => (
                  <DropdownMenu.Item
                    disabled={option.disabled}
                    key={option.value}
                    onSelect={() => {
                      handleSave(option.value);
                    }}
                  >
                    {option.label ? option.label : option.value}
                  </DropdownMenu.Item>
                ))}
              </DropdownMenu.Content>
            </DropdownMenu.Root>
          </Flex>
        </Flex>
      </SettingCard.Action>
    </SettingCard>
  );
}

export function SettingCardLabel({
  children,
}: {
  children: React.ReactNode | null;
}) {
  return (
    <label className="text-xl font-bold" style={{ fontWeight: 600 }}>
      {children}
    </label>
  );
}

export function SettingCardCollapse({
  title,
  description,
  defaultOpen = false,
  children,
  bordless = false,
}: {
  title?: string;
  description?: string;
  children?: React.ReactNode;
  defaultOpen?: boolean;
  bordless?: boolean;
}) {
  const [open, setOpen] = React.useState(defaultOpen);

  return (
    <SettingCard
      title={title}
      description={description}
      onHeaderClick={() => setOpen(!open)}
      bordless={bordless}
    >
      <SettingCard.Action>
        <IconButton
          variant="soft"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          aria-controls="collapsible-content"
        >
          <motion.div
            initial={{ rotate: 0, scale: 1 }}
            animate={{ rotate: open ? 180 : 0, scale: open ? 1.1 : 1 }}
            transition={{ duration: 0.25, ease: [0.4, 0, 0.2, 1] }}
          >
            <ChevronDownIcon />
          </motion.div>
        </IconButton>
      </SettingCard.Action>
      <AnimatePresence>
        {open && (
          <motion.div
            className="w-full p-0 md:p-1" // Ensures the content takes full width
            layout // Smoothly handles height changes
            initial={{ height: 0, opacity: 0, y: -10 }}
            animate={{ height: "auto", opacity: 1, y: 0 }}
            exit={{ height: 0, opacity: 0, y: -10 }}
            transition={{ duration: 0.25, ease: [0.4, 0, 0.2, 1] }}
            style={{ overflow: "hidden" }} // Prevents content clipping during animation
            id="collapsible-content"
          >
            <div className="border-t-1 my-2" />
            {children}
          </motion.div>
        )}
      </AnimatePresence>
    </SettingCard>
  );
}

// Header slot for SettingCardCollapse
SettingCardCollapse.Header = function Header({
  children,
}: {
  children: React.ReactNode;
}) {
  return <div>{children}</div>;
};
