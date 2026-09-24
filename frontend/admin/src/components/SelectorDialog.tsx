import React from "react";
import { Button, Dialog, Flex } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import Selector, { type SelectorProps } from "./Selector";

export interface SelectorDialogProps<T>
  extends Pick<
    SelectorProps<T>,
    | "items"
    | "getId"
    | "getLabel"
    | "sortItems"
    | "filterItem"
    | "searchPlaceholder"
    | "headerLabel"
  > {
  value: string[];
  onChange: (ids: string[]) => void;
  title: React.ReactNode;
  trigger: React.ReactNode;
  className?: string;
}

export function SelectorDialog<T>({
  value,
  onChange,
  title,
  trigger,
  className,
  ...selectorProps
}: SelectorDialogProps<T>) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [temporaryValue, setTemporaryValue] = React.useState(value);

  React.useEffect(() => {
    if (open) setTemporaryValue(value);
  }, [open, value]);

  const handleConfirm = () => {
    onChange(temporaryValue);
    setOpen(false);
  };

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger>{trigger}</Dialog.Trigger>
      <Dialog.Content style={{ maxWidth: 400 }} className={className}>
        <Dialog.Title>{title}</Dialog.Title>
        <Flex direction="column" gap="3">
          <Selector
            {...selectorProps}
            value={temporaryValue}
            onChange={setTemporaryValue}
            hiddenDescription
          />
          <Flex justify="end" gap="2">
            <Dialog.Close>
              <Button variant="soft">{t("common.cancel")}</Button>
            </Dialog.Close>
            <Button onClick={handleConfirm}>{t("common.done")}</Button>
          </Flex>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

export default SelectorDialog;
