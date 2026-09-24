import React from "react";

// 账户数据类型
type Account = {
  logged_in: boolean;
  sso_id: string;
  sso_type: string;
  username: string;
  uuid: string;
  "2fa_enabled": boolean;
};

// Context
interface AccountContextType{
    account: Account | null;
    loading: boolean;
    error: Error | null;
    refresh: () => void;
}

// 创建Context

const AccountContext = React.createContext<AccountContextType | undefined>(undefined);

// Provider组件
export const AccountProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
    const [account, setAccount] = React.useState<Account | null>(null);
    const [loading, setLoading] = React.useState(true);
    const [error, setError] = React.useState<Error | null>(null);

    const refresh = async () => {
        setLoading(true);
        setError(null);
        try {
        const response = await fetch("/api/me");
        if (!response.ok) {
            throw new Error("Failed to fetch account data");
        }
        const data: Account = await response.json();
        setAccount(data);
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
        <AccountContext.Provider value={{ account, loading, error, refresh }}>
        {children}
        </AccountContext.Provider>
    );
}

// 自定义Hook
export const useAccount = () => {
    const context = React.useContext(AccountContext);
    if (!context) {
        throw new Error("useAccount must be used within an AccountProvider");
    }
    return context;
}