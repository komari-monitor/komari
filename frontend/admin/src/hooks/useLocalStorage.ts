import { useCallback, useState } from "react";

export function useLocalStorage<T>(key: string, initialValue: T) {
  const parseStoredValue = (item: string): T => {
    if (typeof initialValue === "string") {
      try {
        const parsed = JSON.parse(item);
        return (typeof parsed === "string" ? parsed : item) as T;
      } catch {
        return item as T;
      }
    }

    return JSON.parse(item);
  };

  // 获取初始值
  const getStoredValue = () => {
    try {
      const item = localStorage.getItem(key);
      return item ? parseStoredValue(item) : initialValue;
    } catch (error) {
      console.warn(`Error reading localStorage key "${key}":`, error);
      return initialValue;
    }
  };

  const [storedValue, setStoredValue] = useState<T>(getStoredValue);

  // 更新存储的值
  const setValue = useCallback((value: T | ((val: T) => T)) => {
    try {
      setStoredValue((currentValue) => {
        const valueToStore =
          value instanceof Function ? value(currentValue) : value;
        localStorage.setItem(
          key,
          typeof valueToStore === "string"
            ? valueToStore
            : JSON.stringify(valueToStore),
        );
        return valueToStore;
      });
    } catch (error) {
      console.warn(`Error setting localStorage key "${key}":`, error);
    }
  }, [key]);

  return [storedValue, setValue] as const;
}
