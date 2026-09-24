import { Flex, IconButton, TextField } from "@radix-ui/themes";
import { Minus, Plus } from "lucide-react";
import React, { useEffect } from "react";

interface NumberPickerProps {
  defaultValue?: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  [key: string]: any; // For additional props
}

export default function NumberPicker({
  defaultValue,
  onChange,
  min = 1,
  max = 100,
  ...props
}: NumberPickerProps) {
  const initialValue = defaultValue !== undefined ? defaultValue : min;
  const clampedInitial = Math.max(min, Math.min(max, initialValue));
  const [value, setValue] = React.useState(String(clampedInitial));

  // Sync defaultValue changes
  useEffect(() => {
    if (defaultValue === undefined) return;
    const numValue = Math.max(min, Math.min(max, defaultValue));
    setValue(String(numValue));
    onChange(numValue);
  }, [defaultValue, min, max, onChange]);

  const handleChange = (newValue: number) => {
    const clampedValue = Math.max(min, Math.min(max, newValue));
    setValue(String(clampedValue));
    onChange(clampedValue);
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const inputValue = e.target.value.trim();
    setValue(inputValue);

    // Only trigger onChange for valid numbers within range
    const numValue = Number(inputValue);
    if (!isNaN(numValue)) {
      if (numValue >= min && numValue <= max) {
        onChange(numValue);
      }
    }
  };

  const handleBlur = () => {
    const numValue = Number(value);
    if (value === "") {
      setValue(String(min));
      onChange(min);
      return;
    }

    const clampedValue = Math.max(min, Math.min(max, numValue));
    setValue(String(clampedValue));
    onChange(clampedValue);
  };

  const currentValue = Number(value) || min;
  const isMinDisabled = currentValue <= min;
  const isMaxDisabled = currentValue >= max;

  return (
    <Flex align="center" gap="2" className="km-ui-number-picker">
      <IconButton
        variant="soft"
        radius="full"
        size="1"
        onClick={() => handleChange(currentValue - 1)}
        disabled={isMinDisabled}
        aria-label="decrement"
      >
        <Minus size="16" />
      </IconButton>
      <TextField.Root
        className="km-ui-number-input"
        type="text"
        inputMode="numeric"
        value={value}
        onChange={handleInputChange}
        onBlur={handleBlur}
        style={{ width: "4rem", textAlign: "center" }}
        {...props}
      >
        <TextField.Slot />
        <TextField.Slot />
      </TextField.Root>
      <IconButton
        variant="soft"
        radius="full"
        size="1"
        onClick={() => handleChange(currentValue + 1)}
        disabled={isMaxDisabled}
        aria-label="increment"
      >
        <Plus size="16" />
      </IconButton>
    </Flex>
  );
}
