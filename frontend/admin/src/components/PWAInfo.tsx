// import { usePWA } from '../hooks/usePWA';
// import { Smartphone, Wifi, Download, RefreshCw } from 'lucide-react';
// import { useTranslation } from 'react-i18next';

export const PWAInfo = () => {
  // const { isInstalled, isStandalone, canInstall, isOnline } = usePWA();
  // const { t } = useTranslation();

  // const StatusBadge = ({ active, children }: { active: boolean; children: React.ReactNode }) => (
  //   <span className={`inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs font-medium ${
  //     active
  //       ? 'bg-green-100 text-green-800 dark:bg-green-900/20 dark:text-green-400'
  //       : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
  //   }`}>
  //     {children}
  //   </span>
  // );

  return (
    // <div className="w-full max-w-md border rounded-lg p-4 bg-white dark:bg-gray-900">
    //   <div className="flex items-center gap-2 mb-4">
    //     <Smartphone size={20} />
    //     <h3 className="font-semibold">{t('pwa.title')}</h3>
    //   </div>
    //   <div className="space-y-3">
    //     <div className="flex items-center justify-between">
    //       <span className="text-sm">{t('pwa.network_status')}</span>
    //       <StatusBadge active={isOnline}>
    //         <Wifi size={12} />
    //         {isOnline ? t('nodeCard.online') : t('nodeCard.offline')}
    //       </StatusBadge>
    //     </div>
    //     <div className="flex items-center justify-between">
    //       <span className="text-sm">{t('pwa.install_status')}</span>
    //       <StatusBadge active={isInstalled}>
    //         <Download size={12} />
    //         {isInstalled ? t('pwa.installed') : t('pwa.not_installed')}
    //       </StatusBadge>
    //     </div>
    //     <div className="flex items-center justify-between">
    //       <span className="text-sm">{t('pwa.running_mode')}</span>
    //       <StatusBadge active={isStandalone}>
    //         <Smartphone size={12} />
    //         {isStandalone ? t('pwa.standalone') : t('pwa.browser')}
    //       </StatusBadge>
    //     </div>
    //     <div className="flex items-center justify-between">
    //       <span className="text-sm">{t('pwa.can_install')}</span>
    //       <StatusBadge active={canInstall}>
    //         <RefreshCw size={12} />
    //         {canInstall ? t('pwa.yes') : t('pwa.no')}
    //       </StatusBadge>
    //     </div>
    //     {isInstalled && (
    //       <div className="text-xs text-gray-600 dark:text-gray-400 pt-2 border-t">
    //         {t('pwa.installed_hint')}
    //       </div>
    //     )}
    //     {!isOnline && (
    //       <div className="text-xs text-orange-600 dark:text-orange-400 pt-2 border-t">
    //         {t('pwa.offline_hint')}
    //       </div>
    //     )}
    //   </div>
    // </div>

    // PWA提示已移除
    <></>
  );
};
