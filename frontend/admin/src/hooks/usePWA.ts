import { useState, useEffect } from 'react';

interface PWAState {
  isInstalled: boolean;
  isStandalone: boolean;
  canInstall: boolean;
  isOnline: boolean;
}

export const usePWA = (): PWAState => {
  const [state, setState] = useState<PWAState>({
    isInstalled: false,
    isStandalone: false,
    canInstall: false,
    isOnline: navigator.onLine
  });

  useEffect(() => {
    // Check if app is in standalone mode
    const isStandalone = window.matchMedia('(display-mode: standalone)').matches;

    // Check if app is installed (rough check)
    const isInstalled = isStandalone ||
      (window.navigator as any).standalone ||
      document.referrer.includes('android-app://');

    setState(prev => ({
      ...prev,
      isStandalone,
      isInstalled
    }));

    // Listen for install prompt
    const handleBeforeInstallPrompt = () => {
      setState(prev => ({ ...prev, canInstall: true }));
    };

    // Listen for app installed
    const handleAppInstalled = () => {
      setState(prev => ({
        ...prev,
        isInstalled: true,
        canInstall: false
      }));
    };

    // Listen for online/offline
    const handleOnline = () => {
      setState(prev => ({ ...prev, isOnline: true }));
    };

    const handleOffline = () => {
      setState(prev => ({ ...prev, isOnline: false }));
    };

    window.addEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
    window.addEventListener('appinstalled', handleAppInstalled);
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);

    return () => {
      window.removeEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
      window.removeEventListener('appinstalled', handleAppInstalled);
      window.removeEventListener('online', handleOnline);
      window.removeEventListener('offline', handleOffline);
    };
  }, []);

  return state;
};
