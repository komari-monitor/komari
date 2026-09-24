import * as React from "react";
import { cn } from "@/lib/utils";
import { TextField } from "@radix-ui/themes";

type Primitive = string | number;

export type SelectOption<T extends Primitive = string> = {
  label: string;
  value: T;
  disabled?: boolean;
};

export type SelectOrInputProps<T extends Primitive = string> = {
  options: Array<SelectOption<T> | T>;
  value?: string;
  defaultValue?: string;
  onChange?: (value: string, option?: SelectOption<T> | undefined) => void;
  placeholder?: string;
  /**
   * Whether to allow custom input (not in options). If provided, this takes precedence.
   * Backward compatible: `allowCustomValue` is kept and will be used only when `allowCustomInput` is undefined.
   */
  allowCustomInput?: boolean;
  /** @deprecated Use `allowCustomInput` instead */
  allowCustomValue?: boolean;
  className?: string;
  listClassName?: string;
  optionClassName?: string;
  emptyText?: string;
  filter?: (option: SelectOption<T>, input: string) => boolean;
  getOptionLabel?: (option: SelectOption<T>) => string;
  getOptionValue?: (option: SelectOption<T>) => string;
  disabled?: boolean;
  name?: string;
  type?: "number" | "search" | "time" | "text" | "hidden" | "date" | "datetime-local" | "email" | "month" | "password" | "tel" | "url" | "week" | undefined;
  // Any other props are passed to the underlying input
} & Omit<
  React.ComponentProps<"input">,
  "value" | "defaultValue" | "onChange" | "placeholder" | "disabled"
>;

/**
 * A combobox-like input: it stays an Input, and shows a floating options list when focused/typing.
 * - Type to filter options
 * - ArrowUp/Down to navigate, Enter to confirm, Escape/Blur to close
 * - Click outside closes the list
 */
export function SelectOrInput<T extends Primitive = string>(
  props: SelectOrInputProps<T>
) {
  const {
    options,
    value,
    defaultValue,
    onChange,
    placeholder,
    allowCustomInput,
    className,
    listClassName,
    optionClassName,
    emptyText = "No results",
    filter,
    getOptionLabel,
    getOptionValue,
    disabled,
    onBlur,
    onFocus,
    onKeyDown,
    type = "text",
    name
  } = props as SelectOrInputProps<T> & {
    onBlur?: React.FocusEventHandler<HTMLInputElement>;
    onFocus?: React.FocusEventHandler<HTMLInputElement>;
    onKeyDown?: React.KeyboardEventHandler<HTMLInputElement>;
  };

  // Normalize options to {label,value}
  const normalizedOptions = React.useMemo<SelectOption<T>[]>(() => {
    return options.map(
      (opt): SelectOption<T> =>
        typeof opt === "string" || typeof opt === "number"
          ? { label: String(opt), value: opt as T }
          : (opt as SelectOption<T>)
    );
  }, [options]);

  const getLabel = React.useCallback(
    (opt: SelectOption<T>) => {
      return getOptionLabel ? getOptionLabel(opt) : opt.label;
    },
    [getOptionLabel]
  );
  const getValue = React.useCallback(
    (opt: SelectOption<T>) => {
      if (getOptionValue) return getOptionValue(opt);
      const v = opt.value;
      return typeof v === "string" ? v : String(v);
    },
    [getOptionValue]
  );

  const containerRef = React.useRef<HTMLDivElement | null>(null);
  const listRef = React.useRef<HTMLUListElement | null>(null);

  const isControlled = value != null;
  const [innerValue, setInnerValue] = React.useState<string>(
    defaultValue ?? ""
  );
  const inputValue = isControlled ? (value as string) : innerValue;

  const [open, setOpen] = React.useState(false);
  const [highlightIndex, setHighlightIndex] = React.useState<number>(-1);

  const allowCustom = (allowCustomInput ?? true) === true;

  const filtered = React.useMemo(() => {
    const text = (inputValue ?? "").trim().toLowerCase();
    const base = normalizedOptions;
    if (!text) return base;
    if (filter) return base.filter((o) => filter(o, inputValue));
    return base.filter((o) => {
      const lbl = getLabel(o).toLowerCase();
      const val = getValue(o).toLowerCase();
      return lbl.includes(text) || val.includes(text);
    });
  }, [normalizedOptions, inputValue, filter, getLabel, getValue]);

  const commit = React.useCallback(
    (next: string, option?: SelectOption<T>) => {
      if (!isControlled) setInnerValue(next);
      onChange?.(next, option);
    },
    [isControlled, onChange]
  );

  // Compute the options to display: if none matched and custom allowed, show current input as a creatable option
  const displayed: SelectOption<T>[] = React.useMemo(() => {
    if (filtered.length > 0) return filtered;
    const text = (inputValue ?? "").trim();
    if (!text) return [];
    if (!allowCustom) return [];
    return [{ label: text, value: text as unknown as T }];
  }, [filtered, inputValue, allowCustom]);

  const selectAt = React.useCallback(
    (index: number) => {
      const opt = displayed[index];
      if (!opt || opt.disabled) return;
      const v = getValue(opt);
      commit(v, opt);
      setOpen(false);
    },
    [displayed, getValue, commit]
  );

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.target.value;
    if (!isControlled) setInnerValue(v);
    onChange?.(v);
    if (!open) setOpen(true);
    setHighlightIndex(0);
  };

  const handleFocus: React.FocusEventHandler<HTMLInputElement> = (e) => {
    setOpen(true);
    onFocus?.(e);
  };

  const handleBlur: React.FocusEventHandler<HTMLInputElement> = (e) => {
    // We'll close on click outside handler; defer here to allow option click
    onBlur?.(e);
  };

  const handleKeyDown: React.KeyboardEventHandler<HTMLInputElement> = (e) => {
    onKeyDown?.(e);
    if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      setOpen(true);
      e.preventDefault();
      return;
    }
    if (!open) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHighlightIndex((i) =>
        Math.min((i < 0 ? -1 : i) + 1, Math.max(displayed.length - 1, 0))
      );
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlightIndex((i) => Math.max((i < 0 ? 0 : i) - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (highlightIndex >= 0 && highlightIndex < displayed.length) {
        selectAt(highlightIndex);
      } else if (allowCustom) {
        commit(inputValue);
        setOpen(false);
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    }
  };

  // Scroll highlighted into view
  React.useEffect(() => {
    if (!listRef.current) return;
    if (highlightIndex < 0) return;
    const el = listRef.current.children[highlightIndex] as
      | HTMLElement
      | undefined;
    if (el) el.scrollIntoView({ block: "nearest" });
  }, [highlightIndex]);

  // Click outside to close
  React.useEffect(() => {
    if (!open) return;
    const onDocDown = (ev: MouseEvent) => {
      const target = ev.target as Node | null;
      if (!containerRef.current) return;
      if (target && containerRef.current.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", onDocDown, { capture: true });
    return () =>
      document.removeEventListener("mousedown", onDocDown, {
        capture: true,
      } as any);
  }, [open]);

  // Build ARIA ids
  const listId = React.useId();

  return (
    <div ref={containerRef} className={cn("km-ui-select-or-input relative", className)}>
      <TextField.Root
        className="km-ui-select"
        //role="combobox"
        //aria-controls={open ? listId : undefined}
        aria-expanded={open}
        aria-autocomplete="list"
        placeholder={placeholder}
        value={inputValue}
        onChange={handleInputChange}
        onFocus={handleFocus}
        onBlur={handleBlur}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        type={type}
        name={name}
        //autoComplete="off"
        //{...inputProps}
      />
      {open && (
        <div
          className={cn(
            "absolute left-0 right-0 z-50 mt-1 rounded-md border bg-accent-1 text-popover-foreground shadow-md",
            "max-h-60 overflow-auto",
            listClassName
          )}
          style={{ minWidth: 0 }}
        >
          <ul id={listId} ref={listRef} role="listbox" className="p-1">
            {displayed.length === 0 ? (
              allowCustom ? null : (
                <li
                  className={cn(
                    "text-muted-foreground select-none rounded-sm px-2 py-1.5 text-sm"
                  )}
                >
                  {emptyText}
                </li>
              )
            ) : (
              displayed.map((opt, idx) => {
                const isActive = idx === highlightIndex;
                const isDisabled = !!opt.disabled;
                return (
                  <li
                    key={`${getValue(opt)}-${idx}`}
                    role="option"
                    aria-selected={isActive}
                    data-disabled={isDisabled || undefined}
                    className={cn(
                      "flex cursor-default select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden font-semibold",
                      "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
                      isActive
                        ? "bg-accent-10 text-accent-foreground"
                        : "hover:bg-accent hover:text-accent-foreground",
                      optionClassName
                    )}
                    onMouseEnter={() => setHighlightIndex(idx)}
                    onMouseDown={(e) => {
                      // prevent input blur before click handler
                      e.preventDefault();
                    }}
                    onClick={() => selectAt(idx)}
                  >
                    {getLabel(opt)}
                  </li>
                );
              })
            )}
          </ul>
        </div>
      )}
    </div>
  );
}

export default SelectOrInput;
