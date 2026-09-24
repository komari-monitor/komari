import React from "react";

export type CommandClipboard = {
  id: number;
  text: string;
  name: string;
  remark: string;
  weight: number;
  createdAt: string;
  updatedAt: string;
};

interface CommandClipboardContextType {
  commands: CommandClipboard[];
  loading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
  addCommand: (name: string, text: string, remark: string, weight: number) => Promise<void>;
  updateCommand: (id: number, name: string, text: string, remark: string, weight: number) => Promise<void>;
  deleteCommand: (id:number) => Promise<void>;
}

const CommandClipboardContext = React.createContext<
  CommandClipboardContextType | undefined
>(undefined);

export const CommandClipboardProvider: React.FC<{
  children: React.ReactNode;
}> = ({ children }) => {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<Error | null>(null);
  const [commands, setCommands] = React.useState<CommandClipboard[]>([]);
  const refresh = async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await fetch("/api/admin/clipboard");
      if (!response.ok) {
        throw new Error("Failed to fetch commands");
      }
      const resp = await response.json();
      if (resp && Array.isArray(resp.data)) {
        setCommands(resp.data);
      } else {
        setCommands([]);
      }
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };
  const addCommand = async (name: string, text: string, remark: string, weight: number) => {
    try {
      const response = await fetch("/api/admin/clipboard", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ name, text, remark, weight }),
      });
      if (!response.ok) {
        throw new Error("Failed to add command");
      }
      refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };

  const updateCommand = async (
    id: number,
    name: string,
    text: string,
    remark: string,
    weight: number
  ) => {
    try {
      const response = await fetch(`/api/admin/clipboard/${id}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ name, text, remark, weight }),
      });
      if (!response.ok) {
        throw new Error("Failed to update command");
      }
      refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };

  const deleteCommand = async (id: number) => {
    try {
      const response = await fetch(`/api/admin/clipboard/${id}/remove`, {
        method: "POST",
      });
      if (!response.ok) {
        throw new Error("Failed to delete command");
      }
      refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };

  React.useEffect(() => {
    refresh();
  }, []);
  return (
    <CommandClipboardContext.Provider
      value={{
        commands,
        loading,
        error,
        refresh,
        addCommand,
        updateCommand,
        deleteCommand,
      }}
    >
      {children}
    </CommandClipboardContext.Provider>
  );
};

export const useCommandClipboard = (): CommandClipboardContextType => {
  const context = React.useContext(CommandClipboardContext);
  if (!context) {
    throw new Error(
      "useCommandClipboard must be used within a CommandClipboardProvider"
    );
  }
  return context;
};
