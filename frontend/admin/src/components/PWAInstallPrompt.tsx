// import { useState, useEffect } from 'react';
// import { Button } from './ui/button';
// import { Download, X } from 'lucide-react';
// import { toast } from 'sonner';
// import { useIsMobile } from '../hooks/use-mobile';

// interface BeforeInstallPromptEvent extends Event {
//     prompt(): Promise<void>;
//     userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
// }
export const PWAInstallPrompt = () => {
    // const [deferredPrompt, setDeferredPrompt] = useState<BeforeInstallPromptEvent | null>(null);
    // const [showMobilePrompt, setShowMobilePrompt] = useState(false);
    // const isMobile = useIsMobile();

    // useEffect(() => {
    //     const handleBeforeInstallPrompt = (e: Event) => {
    //         e.preventDefault();
    //         setDeferredPrompt(e as BeforeInstallPromptEvent);

    //         if (isMobile) {
    //             // 在移动端显示更显眼的全屏提示
    //             setShowMobilePrompt(true);
    //         } else {
    //             // 桌面端继续使用toast
    //             toast.info(
    //                 <>
    //                     <div className="flex items-center gap-3">
    //                         <div>
    //                             <h3 className="font-semibold text-sm">安装应用</h3>
    //                             <p className="text-xs text-gray-600 dark:text-gray-400">将应用安装到您的设备上</p>
    //                         </div>
    //                         <div className="flex gap-2">
    //                             <Button size="sm" variant="outline" onClick={() => toast.dismiss()}> 稍后 </Button>
    //                             <Button size="sm" onClick={handleInstallClick} className="flex items-center gap-1"> <Download size={14} /> 安装 </Button>
    //                         </div>
    //                     </div>
    //                 </>
    //             );
    //         }
    //     };

    //     const handleAppInstalled = () => {
    //         console.log('PWA was installed');
    //         setDeferredPrompt(null);
    //         setShowMobilePrompt(false);
    //     };

    //     // 检查是否已经安装
    //     const isInstalled = window.matchMedia('(display-mode: standalone)').matches ||
    //         (window.navigator as any).standalone === true ||
    //         document.referrer.includes('android-app://');

    //     if (isInstalled) {
    //         return;
    //     }

    //     window.addEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
    //     window.addEventListener('appinstalled', handleAppInstalled);

    //     // 如果在移动端并且一定时间后还没有收到beforeinstallprompt事件，
    //     // 可能是因为浏览器限制，我们可以显示一个通用的安装提示
    //     let fallbackTimer: NodeJS.Timeout;
    //     if (isMobile) {
    //         fallbackTimer = setTimeout(() => {
    //             if (!deferredPrompt && !isInstalled) {
    //                 // 显示通用安装指南
    //                 console.log('Showing fallback install prompt for mobile');
    //                 setShowMobilePrompt(true);
    //             }
    //         }, 3000); // 3秒后如果没有收到原生事件，显示后备提示
    //     }

    //     return () => {
    //         window.removeEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
    //         window.removeEventListener('appinstalled', handleAppInstalled);
    //         if (fallbackTimer) {
    //             clearTimeout(fallbackTimer);
    //         }
    //     };
    //     // eslint-disable-next-line react-hooks/exhaustive-deps
    // }, [isMobile]);

    // const handleInstallClick = async () => {
    //     if (deferredPrompt) {
    //         // 有原生安装提示的情况
    //         deferredPrompt.prompt();
    //         const { outcome } = await deferredPrompt.userChoice;
    //         if (outcome === 'accepted') {
    //             console.log('User accepted the install prompt');
    //         } else {
    //             console.log('User dismissed the install prompt');
    //         }
    //         setDeferredPrompt(null);
    //         setShowMobilePrompt(false);
    //         toast.dismiss();
    //     } else {
    //         // 没有原生安装提示，显示手动安装指南
    //         handleShowInstallGuide();
    //     }
    // };

    // const handleShowInstallGuide = () => {
    //     const isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent);
    //     const isAndroid = /Android/.test(navigator.userAgent);

    //     let guideText = '';
    //     if (isIOS) {
    //         guideText = '点击浏览器底部的分享按钮，然后选择"添加到主屏幕"';
    //     } else if (isAndroid) {
    //         guideText = '点击浏览器菜单（三个点），然后选择"安装应用"或"添加到主屏幕"';
    //     } else {
    //         guideText = '请查看浏览器菜单中的"安装"或"添加到主屏幕"选项';
    //     }

    //     toast.info(guideText, {
    //         duration: 5000,
    //     });
    //     setShowMobilePrompt(false);
    // };

    // const handleDismiss = () => {
    //     setShowMobilePrompt(false);
    //     setDeferredPrompt(null);
    // };

    // // 移动端显示全屏提示
    // if (showMobilePrompt && isMobile) {
    //     const hasNativePrompt = !!deferredPrompt;

    //     return (
    //         <div className="fixed inset-0 z-50 bg-black/50 flex items-end">
    //             <div className="w-full bg-white dark:bg-gray-900 rounded-t-lg p-6 animate-in slide-in-from-bottom duration-300">
    //                 <div className="flex justify-between items-start mb-4">
    //                     <div>
    //                         <h2 className="text-lg font-bold text-gray-900 dark:text-white">
    //                             安装应用到主屏幕
    //                         </h2>
    //                         <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
    //                             {hasNativePrompt
    //                                 ? '快速访问 Komari Monitor，享受更好的体验'
    //                                 : '将应用添加到主屏幕，方便快速访问'
    //                             }
    //                         </p>
    //                     </div>
    //                     <Button
    //                         variant="ghost"
    //                         size="sm"
    //                         onClick={handleDismiss}
    //                         className="text-gray-500 hover:text-gray-700"
    //                     >
    //                         <X size={20} />
    //                     </Button>
    //                 </div>

    //                 <div className="flex gap-3">
    //                     <Button
    //                         variant="outline"
    //                         onClick={handleDismiss}
    //                         className="flex-1"
    //                     >
    //                         稍后
    //                     </Button>
    //                     <Button
    //                         onClick={handleInstallClick}
    //                         className="flex-1 flex items-center justify-center gap-2"
    //                     >
    //                         <Download size={16} />
    //                         {hasNativePrompt ? '安装' : '查看指南'}
    //                     </Button>
    //                 </div>
    //             </div>
    //         </div>
    //     );
    // }

    // 桌面端不渲染任何内容，全部通过 toast 展示


    // PWA提示已移除，让用户手动控制安装
    return null;
};
