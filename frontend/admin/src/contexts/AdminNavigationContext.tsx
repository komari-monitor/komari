import React from "react";

interface AdminNavigationContextValue {
  refreshVersion: number;
  refreshNavigation: () => void;
}

const AdminNavigationContext = React.createContext<
  AdminNavigationContextValue | undefined
>(undefined);

export const AdminNavigationProvider: React.FC<{
  children: React.ReactNode;
}> = ({ children }) => {
  const [refreshVersion, setRefreshVersion] = React.useState(0);

  const refreshNavigation = React.useCallback(() => {
    setRefreshVersion((version) => version + 1);
  }, []);

  const value = React.useMemo(
    () => ({ refreshVersion, refreshNavigation }),
    [refreshVersion, refreshNavigation],
  );

  return (
    <AdminNavigationContext.Provider value={value}>
      {children}
    </AdminNavigationContext.Provider>
  );
};

export const useAdminNavigation = () => {
  const context = React.useContext(AdminNavigationContext);
  if (!context) {
    throw new Error(
      "useAdminNavigation must be used within an AdminNavigationProvider",
    );
  }
  return context;
};
