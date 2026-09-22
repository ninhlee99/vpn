import React, { useState, useEffect, useRef } from 'react';
import { Wifi, Battery, Search, Sliders, Shield, ShieldCheck, ShieldAlert, Settings, Plus, LogOut, Info, ShieldQuestion } from 'lucide-react';
import { ConnectionState } from '../types';

interface MacOSMenuBarProps {
  isPopoverOpen: boolean;
  onTogglePopover: () => void;
  connectionState: ConnectionState;
  activeProfileName: string | null;
  onOpenSettings: () => void;
  onOpenAddModal?: () => void;
  onQuitApp?: () => void;
}

export const MacOSMenuBar: React.FC<MacOSMenuBarProps> = ({
  isPopoverOpen,
  onTogglePopover,
  connectionState,
  activeProfileName,
  onOpenSettings,
  onOpenAddModal,
  onQuitApp
}) => {
  const [timeString, setTimeString] = useState<string>('');
  const [activeMenu, setActiveMenu] = useState<string | null>(null);
  const menuBarRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const updateTime = () => {
      const now = new Date();
      const days = ['CN', 'Th 2', 'Th 3', 'Th 4', 'Th 5', 'Th 6', 'Th 7'];
      const day = days[now.getDay()];
      const hours = now.getHours().toString().padStart(2, '0');
      const mins = now.getMinutes().toString().padStart(2, '0');
      setTimeString(`${day} ${hours}:${mins}`);
    };
    updateTime();
    const interval = setInterval(updateTime, 10000);
    return () => clearInterval(interval);
  }, []);

  // Close active dropdown if clicked outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (menuBarRef.current && !menuBarRef.current.contains(event.target as Node)) {
        setActiveMenu(null);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const renderVpnIcon = () => {
    switch (connectionState) {
      case 'connected':
        return (
          <div className="relative flex items-center justify-center">
            <ShieldCheck className="w-[16px] h-[16px] text-emerald-400 fill-emerald-400/25 filter drop-shadow-[0_0_4px_rgba(52,211,153,0.4)]" />
            <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 rounded-full bg-emerald-400 shadow-[0_0_6px_#34d399] border border-[#161a22]" />
          </div>
        );
      case 'connecting':
        return (
          <div className="relative flex items-center justify-center animate-pulse">
            <Shield className="w-[16px] h-[16px] text-amber-400 fill-amber-400/25" />
            <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 rounded-full bg-amber-400 shadow-[0_0_6px_#fbbf24] border border-[#161a22]" />
          </div>
        );
      case 'reconnecting':
        return (
          <div className="relative flex items-center justify-center">
            <ShieldAlert className="w-[16px] h-[16px] text-amber-400 fill-amber-400/25 animate-pulse" />
            <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 rounded-full bg-amber-500 shadow-[0_0_6px_#f59e0b] border border-[#161a22]" />
          </div>
        );
      case 'error':
        return (
          <div className="relative flex items-center justify-center">
            <ShieldAlert className="w-[16px] h-[16px] text-rose-400 fill-rose-400/25" />
            <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 rounded-full bg-rose-500 shadow-[0_0_6px_#f43f5e] border border-[#161a22]" />
          </div>
        );
      case 'disconnected':
      default:
        return (
          <div className="relative flex items-center justify-center">
            <Shield className="w-[16px] h-[16px] text-slate-300/80 hover:text-white transition-colors" />
          </div>
        );
    }
  };

  return (
    <div
      ref={menuBarRef}
      id="macos-menubar"
      className="w-full h-8 bg-[#161a22]/90 backdrop-blur-xl border-b border-white/[0.08] px-3.5 flex items-center justify-between text-[13px] font-medium text-slate-200 select-none z-40 relative shadow-sm"
    >
      {/* Left Menu Items (Apple logo, App Name, Menus) */}
      <div className="flex items-center gap-1">
        {/* Apple Menu */}
        <div className="relative">
          <button
            onClick={() => setActiveMenu(activeMenu === 'apple' ? null : 'apple')}
            className={`px-2 py-0.5 rounded text-[15px] transition-colors ${
              activeMenu === 'apple' ? 'bg-white/20 text-white' : 'hover:bg-white/10 text-white/90'
            }`}
          >
            
          </button>
          {activeMenu === 'apple' && (
            <div className="absolute top-8 left-0 w-56 rounded-xl bg-[#1a212d]/95 backdrop-blur-xl border border-white/10 shadow-2xl py-1 z-50 text-[13px] text-slate-200">
              <div className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer">
                Giới thiệu về máy Mac này
              </div>
              <div className="my-1 border-t border-white/10" />
              <div
                onClick={() => {
                  setActiveMenu(null);
                  onOpenSettings();
                }}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between"
              >
                <span>Cài đặt hệ thống...</span>
              </div>
            </div>
          )}
        </div>

        {/* TMS-VPN App Menu with Settings ⌘, */}
        <div className="relative">
          <button
            onClick={() => setActiveMenu(activeMenu === 'app' ? null : 'app')}
            className={`px-2 py-0.5 rounded font-bold tracking-tight transition-colors ${
              activeMenu === 'app' ? 'bg-white/20 text-white' : 'hover:bg-white/10 text-white'
            }`}
          >
            TMS-VPN
          </button>
          {activeMenu === 'app' && (
            <div className="absolute top-8 left-0 w-60 rounded-xl bg-[#1a212d]/95 backdrop-blur-xl border border-white/10 shadow-2xl py-1 z-50 text-[13px] text-slate-200">
              <div
                onClick={() => setActiveMenu(null)}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center gap-2"
              >
                <Info className="w-3.5 h-3.5 opacity-70" />
                <span>Giới thiệu TMS-VPN</span>
              </div>
              <div className="my-1 border-t border-white/10" />
              {/* Settings Item */}
              <div
                id="menu-item-settings"
                onClick={() => {
                  setActiveMenu(null);
                  onOpenSettings();
                }}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between font-medium"
              >
                <div className="flex items-center gap-2">
                  <Settings className="w-3.5 h-3.5 text-cyan-400" />
                  <span>Cài đặt...</span>
                </div>
                <span className="text-[12px] opacity-60">⌘,</span>
              </div>
              <div className="my-1 border-t border-white/10" />
              <div
                onClick={() => setActiveMenu(null)}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between"
              >
                <span>Ẩn TMS-VPN</span>
                <span className="text-[12px] opacity-60">⌘H</span>
              </div>
              <div className="my-1 border-t border-white/10" />
              <div
                onClick={() => {
                  setActiveMenu(null);
                  if (onQuitApp) onQuitApp();
                }}
                className="px-3 py-1.5 hover:bg-red-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between text-red-300"
              >
                <div className="flex items-center gap-2">
                  <LogOut className="w-3.5 h-3.5" />
                  <span>Thoát TMS-VPN</span>
                </div>
                <span className="text-[12px] opacity-60">⌘Q</span>
              </div>
            </div>
          )}
        </div>

        {/* Profile Menu */}
        <div className="relative">
          <button
            onClick={() => setActiveMenu(activeMenu === 'profile' ? null : 'profile')}
            className={`px-2 py-0.5 rounded transition-colors ${
              activeMenu === 'profile' ? 'bg-white/20 text-white' : 'hover:bg-white/10 text-slate-200'
            }`}
          >
            Hồ sơ
          </button>
          {activeMenu === 'profile' && (
            <div className="absolute top-8 left-0 w-56 rounded-xl bg-[#1a212d]/95 backdrop-blur-xl border border-white/10 shadow-2xl py-1 z-50 text-[13px] text-slate-200">
              <div
                onClick={() => {
                  setActiveMenu(null);
                  if (onOpenAddModal) onOpenAddModal();
                }}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between"
              >
                <div className="flex items-center gap-2">
                  <Plus className="w-3.5 h-3.5 text-cyan-400" />
                  <span>Thêm điểm nối mới...</span>
                </div>
                <span className="text-[12px] opacity-60">⌘N</span>
              </div>
            </div>
          )}
        </div>

        {/* Window / Cửa sổ Menu */}
        <div className="relative">
          <button
            onClick={() => setActiveMenu(activeMenu === 'window' ? null : 'window')}
            className={`px-2 py-0.5 rounded transition-colors ${
              activeMenu === 'window' ? 'bg-white/20 text-white' : 'hover:bg-white/10 text-slate-200'
            }`}
          >
            Cửa sổ
          </button>
          {activeMenu === 'window' && (
            <div className="absolute top-8 left-0 w-56 rounded-xl bg-[#1a212d]/95 backdrop-blur-xl border border-white/10 shadow-2xl py-1 z-50 text-[13px] text-slate-200">
              <div
                onClick={() => {
                  setActiveMenu(null);
                  onTogglePopover();
                }}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between"
              >
                <span>Bảng điều khiển Menu Bar</span>
                <span className="text-[12px] opacity-60">⌘1</span>
              </div>
              <div
                onClick={() => {
                  setActiveMenu(null);
                  onOpenSettings();
                }}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center justify-between"
              >
                <span>Cài đặt & Tùy chọn...</span>
                <span className="text-[12px] opacity-60">⌘,</span>
              </div>
            </div>
          )}
        </div>

        {/* Help Menu */}
        <div className="relative">
          <button
            onClick={() => setActiveMenu(activeMenu === 'help' ? null : 'help')}
            className={`px-2 py-0.5 rounded transition-colors ${
              activeMenu === 'help' ? 'bg-white/20 text-white' : 'hover:bg-white/10 text-slate-200'
            }`}
          >
            Trợ giúp
          </button>
          {activeMenu === 'help' && (
            <div className="absolute top-8 left-0 w-60 rounded-xl bg-[#1a212d]/95 backdrop-blur-xl border border-white/10 shadow-2xl py-1 z-50 text-[13px] text-slate-200">
              <div
                onClick={() => setActiveMenu(null)}
                className="px-3 py-1.5 hover:bg-blue-600 hover:text-white rounded-md mx-1 cursor-pointer flex items-center gap-2"
              >
                <ShieldQuestion className="w-3.5 h-3.5 text-cyan-400" />
                <span>Hướng dẫn cấu hình L2TP macOS</span>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Right Menu Items (System Tray + TMS-VPN Status Icon) */}
      <div className="flex items-center gap-3">
        {/* TMS-VPN Menu Bar Status Button (Target Trigger) */}
        <button
          id="menubar-tms-vpn-trigger"
          onClick={onTogglePopover}
          className={`flex items-center justify-center p-1.5 rounded-md transition-all ${
            isPopoverOpen
              ? 'bg-white/20 text-white shadow-inner ring-1 ring-white/20'
              : 'hover:bg-white/10 text-slate-200'
          }`}
          title="TMS-VPN"
        >
          {renderVpnIcon()}
        </button>

        {/* Standard macOS Menu Bar Icons */}
        <div className="flex items-center gap-2.5 text-slate-300">
          <Wifi className="w-3.5 h-3.5 hover:text-white cursor-default" />
          <Battery className="w-4 h-4 hover:text-white cursor-default" />
          <Search className="w-3.5 h-3.5 hover:text-white cursor-default" />
          <Sliders className="w-3.5 h-3.5 hover:text-white cursor-default" />
          <span className="text-[12.5px] tracking-tight cursor-default pl-1 text-slate-100 font-normal">
            {timeString}
          </span>
        </div>
      </div>
    </div>
  );
};

