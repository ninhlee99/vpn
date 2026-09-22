import React, { useState, useEffect } from 'react';
import {
  Shield,
  Plus,
  MoreHorizontal,
  LogOut,
  Trash2,
  Edit3,
  ChevronLeft,
  X
} from 'lucide-react';
import { VPNProfile, ConnectionState, AppSettings } from '../types';

export type PopoverViewMode = 'list' | 'add' | 'edit';

interface TMSVPNPopoverProps {
  profiles: VPNProfile[];
  activeProfileId: string | null;
  connectionState: ConnectionState;
  settings: AppSettings;
  viewMode?: PopoverViewMode;
  arrowLeft?: number;
  onViewModeChange?: (mode: PopoverViewMode) => void;
  onClosePopover: () => void;
  onToggleProfile: (profile: VPNProfile) => void;
  onAddProfile: (newProfile: Omit<VPNProfile, 'id' | 'latencyMs'>) => void;
  onSaveProfile: (updated: VPNProfile) => void;
  onDeleteProfile: (id: string) => void;
  onUpdateSettings: (newSettings: Partial<AppSettings>) => void;
  onQuitApp: () => void;
  onToggleReconnecting?: (id: string) => void;
}

export const TMSVPNPopover: React.FC<TMSVPNPopoverProps> = ({
  profiles,
  activeProfileId,
  connectionState,
  settings,
  viewMode: controlledViewMode,
  arrowLeft,
  onViewModeChange,
  onClosePopover,
  onToggleProfile,
  onAddProfile,
  onSaveProfile,
  onDeleteProfile,
  onUpdateSettings,
  onQuitApp,
  onToggleReconnecting
}) => {
  const [internalViewMode, setInternalViewMode] = useState<PopoverViewMode>('list');
  const currentView = controlledViewMode ?? internalViewMode;

  const setViewMode = (mode: PopoverViewMode) => {
    if (onViewModeChange) {
      onViewModeChange(mode);
    } else {
      setInternalViewMode(mode);
    }
  };

  const [activeMenuId, setActiveMenuId] = useState<string | null>(null);
  const [editingProfile, setEditingProfile] = useState<VPNProfile | null>(null);

  // Add Form State
  const [addName, setAddName] = useState('');
  const [addServer, setAddServer] = useState('');
  const [addUser, setAddUser] = useState('');
  const [addSecret, setAddSecret] = useState('');
  const [addSendAllTraffic, setAddSendAllTraffic] = useState(true);

  // Edit Form State
  const [editName, setEditName] = useState('');
  const [editServer, setEditServer] = useState('');
  const [editUser, setEditUser] = useState('');
  const [editSecret, setEditSecret] = useState('');
  const [editSendAllTraffic, setEditSendAllTraffic] = useState(true);

  useEffect(() => {
    const handleClickOutside = () => setActiveMenuId(null);
    window.addEventListener('click', handleClickOutside);
    return () => window.removeEventListener('click', handleClickOutside);
  }, []);

  // Keyboard shortcut listener for Escape to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (currentView !== 'list') {
          setViewMode('list');
        } else {
          onClosePopover();
        }
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [currentView, onClosePopover]);

  const handleStartEdit = (profile: VPNProfile) => {
    setEditingProfile(profile);
    setEditName(profile.name);
    setEditServer(profile.serverAddress);
    setEditUser(profile.username || '');
    setEditSecret(profile.sharedSecret || '');
    setEditSendAllTraffic(profile.sendAllTraffic ?? true);
    setViewMode('edit');
    setActiveMenuId(null);
  };

  const handleSaveEditSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingProfile || !editName.trim() || !editServer.trim()) return;
    onSaveProfile({
      ...editingProfile,
      name: editName.trim(),
      serverAddress: editServer.trim(),
      username: editUser.trim() || undefined,
      sharedSecret: editSecret.trim() || undefined,
      sendAllTraffic: editSendAllTraffic
    });
    setViewMode('list');
    setEditingProfile(null);
  };

  const handleAddSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!addName.trim() || !addServer.trim()) return;
    onAddProfile({
      name: addName.trim(),
      serverAddress: addServer.trim(),
      username: addUser.trim() || undefined,
      sharedSecret: addSecret.trim() || undefined,
      sendAllTraffic: addSendAllTraffic,
      isEnabled: false,
      isReconnecting: false
    });
    // Reset
    setAddName('');
    setAddServer('');
    setAddUser('');
    setAddSecret('');
    setAddSendAllTraffic(true);
    setViewMode('list');
  };

  const getStatusText = () => {
    switch (connectionState) {
      case 'connected': return 'Đang kết nối';
      case 'connecting': return 'Đang kết nối...';
      case 'reconnecting': return 'Đang thử kết nối lại...';
      case 'disconnecting': return 'Đang ngắt...';
      case 'disconnected': return 'Đã ngắt kết nối';
      case 'error': return 'Lỗi kết nối';
    }
  };

  const getStatusColor = () => {
    switch (connectionState) {
      case 'connected': return '#10b981';
      case 'connecting': return '#f59e0b';
      case 'reconnecting': return '#ef4444';
      case 'disconnecting': return '#6b7280';
      case 'disconnected': return '#6b7280';
      case 'error': return '#ef4444';
    }
  };

  return (
    <div
      id="tms-vpn-popover-container"
      className="relative w-[360px] sm:w-[380px] rounded-[22px] bg-[#12161f]/95 text-slate-100 backdrop-blur-2xl border border-white/10 shadow-[0_25px_60px_rgba(0,0,0,0.8),0_0_1px_rgba(255,255,255,0.2)] overflow-visible select-none transition-all duration-200"
    >
      {/* Popover Arrow / Notch pointing up directly to macOS status bar */}
      <div
        className="absolute -top-[7px] w-3.5 h-3.5 -translate-x-1/2 rotate-45 bg-[#12161f] border-t border-l border-white/15 rounded-tl-sm pointer-events-none z-20 transition-all duration-150"
        style={{ left: arrowLeft !== undefined ? `${arrowLeft}px` : 'calc(100% - 78px)' }}
      />

      {/* ========================================================================= */}
      {/* VIEW 1: ADD PROFILE FORM (THÊM ĐIỂM NỐI TRỰC TIẾP TRONG POPOVER) */}
      {/* ========================================================================= */}
      {currentView === 'add' && (
        <form onSubmit={handleAddSubmit} className="animate-in fade-in slide-in-from-right-3 duration-150">
          {/* Header with Back and Close X */}
          <div className="px-4 py-3.5 border-b border-white/[0.08] flex items-center justify-between bg-white/[0.02]">
            <button
              type="button"
              onClick={() => setViewMode('list')}
              className="flex items-center gap-1 text-[12.5px] font-medium text-cyan-400 hover:text-cyan-300 transition-colors py-1 px-1.5 -ml-1 rounded-md hover:bg-white/5"
            >
              <ChevronLeft className="w-4 h-4" />
              <span>Quay lại</span>
            </button>
            <span className="text-[14px] font-semibold text-white">Thêm Điểm Nối L2TP</span>
            <button
              type="button"
              onClick={onClosePopover}
              title="Đóng cửa sổ"
              className="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-white/10 transition-colors -mr-1"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          {/* Form Fields */}
          <div className="p-4 space-y-3 text-[13px]">
            <div>
              <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                Tên điểm nối *
              </label>
              <input
                type="text"
                required
                placeholder="VD: Chi nhánh Hà Nội, Server Staging..."
                value={addName}
                onChange={(e) => setAddName(e.target.value)}
                className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
              />
            </div>

            <div>
              <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                Địa chỉ máy chủ (Host / IP) *
              </label>
              <input
                type="text"
                required
                placeholder="vpn.company.internal hoặc IP"
                value={addServer}
                onChange={(e) => setAddServer(e.target.value)}
                className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
              />
            </div>

            <div className="grid grid-cols-2 gap-2.5">
              <div>
                <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                  Tài khoản
                </label>
                <input
                  type="text"
                  placeholder="Username"
                  value={addUser}
                  onChange={(e) => setAddUser(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
                />
              </div>

              <div>
                <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                  Khóa bí mật (Secret)
                </label>
                <input
                  type="password"
                  placeholder="Shared Secret"
                  value={addSecret}
                  onChange={(e) => setAddSecret(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
                />
              </div>
            </div>

            {/* Toggle Send All Traffic */}
            <div className="pt-1">
              <div className="p-3 bg-black/30 rounded-xl border border-white/5 flex items-center justify-between">
                <div className="pr-3">
                  <div className="text-[12.5px] font-medium text-white">Gửi toàn bộ traffic</div>
                  <div className="text-[11px] text-slate-400">Định tuyến tất cả lưu lượng Internet qua VPN</div>
                </div>
                <button
                  type="button"
                  onClick={() => setAddSendAllTraffic(!addSendAllTraffic)}
                  className={`relative inline-flex h-[20px] w-[36px] items-center rounded-full transition-colors duration-200 focus:outline-none flex-shrink-0 ${
                    addSendAllTraffic ? 'bg-[#3b82f6]' : 'bg-[#334155]'
                  }`}
                >
                  <span
                    className={`inline-block h-[14px] w-[14px] transform rounded-full bg-white transition-transform duration-200 shadow-md ${
                      addSendAllTraffic ? 'translate-x-[19px]' : 'translate-x-[3px]'
                    }`}
                  />
                </button>
              </div>
            </div>
          </div>

          {/* Form Actions */}
          <div className="px-4 py-3 border-t border-white/[0.08] flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={() => setViewMode('list')}
              className="px-3 py-1.5 rounded-xl text-[12px] font-medium text-slate-300 hover:text-white hover:bg-white/5 transition-colors"
            >
              Hủy
            </button>
            <button
              type="submit"
              className="px-4 py-1.5 rounded-xl text-[12.5px] font-semibold text-white bg-blue-600 hover:bg-blue-500 transition-colors shadow-sm"
            >
              Lưu điểm nối
            </button>
          </div>
        </form>
      )}

      {/* ========================================================================= */}
      {/* VIEW 3: EDIT PROFILE FORM (CHỈNH SỬA HỒ SƠ TRỰC TIẾP TRONG POPOVER) */}
      {/* ========================================================================= */}
      {currentView === 'edit' && editingProfile && (
        <form onSubmit={handleSaveEditSubmit} className="animate-in fade-in slide-in-from-right-3 duration-150">
          {/* Header with Back and Close X */}
          <div className="px-4 py-3.5 border-b border-white/[0.08] flex items-center justify-between bg-white/[0.02]">
            <button
              type="button"
              onClick={() => {
                setViewMode('list');
                setEditingProfile(null);
              }}
              className="flex items-center gap-1 text-[12.5px] font-medium text-cyan-400 hover:text-cyan-300 transition-colors py-1 px-1.5 -ml-1 rounded-md hover:bg-white/5"
            >
              <ChevronLeft className="w-4 h-4" />
              <span>Quay lại</span>
            </button>
            <span className="text-[14px] font-semibold text-white">Chỉnh Sửa Hồ Sơ</span>
            <button
              type="button"
              onClick={onClosePopover}
              title="Đóng cửa sổ"
              className="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-white/10 transition-colors -mr-1"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          {/* Form Fields */}
          <div className="p-4 space-y-3 text-[13px]">
            <div>
              <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                Tên điểm nối *
              </label>
              <input
                type="text"
                required
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
              />
            </div>

            <div>
              <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                Địa chỉ máy chủ (Host / IP) *
              </label>
              <input
                type="text"
                required
                value={editServer}
                onChange={(e) => setEditServer(e.target.value)}
                className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
              />
            </div>

            <div className="grid grid-cols-2 gap-2.5">
              <div>
                <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                  Tài khoản
                </label>
                <input
                  type="text"
                  placeholder="Username"
                  value={editUser}
                  onChange={(e) => setEditUser(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
                />
              </div>

              <div>
                <label className="block text-[11px] font-medium text-[#8e9aa8] mb-1 uppercase tracking-wider">
                  Khóa bí mật (Secret)
                </label>
                <input
                  type="password"
                  placeholder="Shared Secret"
                  value={editSecret}
                  onChange={(e) => setEditSecret(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-black/40 border border-white/10 text-white placeholder-slate-500 focus:outline-none focus:border-cyan-400 text-[13px]"
                />
              </div>
            </div>

            {/* Toggle Send All Traffic */}
            <div className="pt-1">
              <div className="p-3 bg-black/30 rounded-xl border border-white/5 flex items-center justify-between">
                <div className="pr-3">
                  <div className="text-[12.5px] font-medium text-white">Gửi toàn bộ traffic</div>
                  <div className="text-[11px] text-slate-400">Định tuyến tất cả lưu lượng Internet qua VPN</div>
                </div>
                <button
                  type="button"
                  onClick={() => setEditSendAllTraffic(!editSendAllTraffic)}
                  className={`relative inline-flex h-[20px] w-[36px] items-center rounded-full transition-colors duration-200 focus:outline-none flex-shrink-0 ${
                    editSendAllTraffic ? 'bg-[#3b82f6]' : 'bg-[#334155]'
                  }`}
                >
                  <span
                    className={`inline-block h-[14px] w-[14px] transform rounded-full bg-white transition-transform duration-200 shadow-md ${
                      editSendAllTraffic ? 'translate-x-[19px]' : 'translate-x-[3px]'
                    }`}
                  />
                </button>
              </div>
            </div>
          </div>

          {/* Form Actions */}
          <div className="px-4 py-3 border-t border-white/[0.08] flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={() => {
                setViewMode('list');
                setEditingProfile(null);
              }}
              className="px-3 py-1.5 rounded-xl text-[12px] font-medium text-slate-300 hover:text-white hover:bg-white/5 transition-colors"
            >
              Hủy
            </button>
            <button
              type="submit"
              className="px-4 py-1.5 rounded-xl text-[12.5px] font-semibold text-white bg-blue-600 hover:bg-blue-500 transition-colors shadow-sm"
            >
              Lưu thay đổi
            </button>
          </div>
        </form>
      )}

      {/* ========================================================================= */}
      {/* VIEW 4: MAIN LIST VIEW (DANH SÁCH HỒ SƠ VPN) */}
      {/* ========================================================================= */}
      {currentView === 'list' && (
        <div className="animate-in fade-in duration-100">
          {/* Header Bar - Contains ONLY ONE Close (X) button, NO duplicate settings icon */}
          <div id="popover-header" className="px-5 pt-4 pb-3.5 flex items-center justify-between">
            <div className="flex items-center gap-3">
              {/* Shield Badge */}
              <div
                className={`relative w-11 h-11 rounded-[14px] flex items-center justify-center transition-all duration-300 ${
                  connectionState === 'connecting' || connectionState === 'reconnecting'
                    ? 'overflow-hidden p-[2px] shadow-[0_0_20px_rgba(245,158,11,0.5)]'
                    : connectionState === 'connected'
                    ? 'border-2 border-emerald-400 bg-emerald-950/60 shadow-[0_0_18px_rgba(52,211,153,0.5)] animate-pulse'
                    : 'bg-gradient-to-br from-[#0c2e3a] to-[#061922] border border-[#196b7d]/70 shadow-[0_0_16px_rgba(20,184,210,0.35)]'
                }`}
              >
                {(connectionState === 'connecting' || connectionState === 'reconnecting') && (
                  <>
                    <div className="absolute -inset-[150%] animate-spin-slow bg-[conic-gradient(from_0deg,transparent_0_260deg,#f59e0b_310deg,#fbbf24_360deg)]" />
                    <div className="absolute inset-[2px] rounded-[12px] bg-[#241306]/95 z-0" />
                  </>
                )}
                <div className="relative z-10 flex items-center justify-center">
                  <Shield
                    className={`w-5 h-5 stroke-[2.2] transition-colors duration-300 ${
                      connectionState === 'connecting' || connectionState === 'reconnecting'
                        ? 'text-amber-400 fill-amber-400/25 animate-pulse'
                        : connectionState === 'connected'
                        ? 'text-emerald-400 fill-emerald-400/30'
                        : 'text-[#22d3ee] fill-[#22d3ee]/20'
                    }`}
                  />
                  <span
                    className={`absolute -top-1 -right-1.5 w-2 h-2 rounded-full transition-colors duration-300 ${
                      connectionState === 'connecting' || connectionState === 'reconnecting'
                        ? 'bg-amber-400 shadow-[0_0_6px_#fbbf24]'
                        : connectionState === 'connected'
                        ? 'bg-[#34d399] shadow-[0_0_6px_#34d399]'
                        : 'bg-[#64748b]'
                    }`}
                  />
                </div>
              </div>

              <div className="flex items-center gap-2">
                <span className="text-[17px] font-bold tracking-tight text-white">TMS-VPN</span>
                <span
                  className="w-2 h-2 rounded-full transition-colors duration-300 ml-0.5"
                  style={{
                    backgroundColor: getStatusColor(),
                    boxShadow: `0 0 8px ${getStatusColor()}`
                  }}
                />
                <span
                  className="text-[13px] font-medium transition-colors duration-300"
                  style={{ color: getStatusColor() }}
                >
                  {getStatusText()}
                </span>
              </div>
            </div>

            {/* Single Close Button (X) in the header */}
            <button
              id="btn-close-popover"
              onClick={onClosePopover}
              title="Đóng (Esc)"
              className="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-white/10 transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          <div className="h-[1px] bg-white/[0.08] mx-0" />

          {/* Profile Section Header */}
          <div id="popover-profile-header" className="px-5 pt-4 pb-2.5 flex items-center justify-between">
            <span className="text-[11px] font-bold tracking-wider text-[#8e9aa8] uppercase">
              HỒ SƠ VPN CÔNG TY
            </span>
            <button
              id="btn-add-profile"
              onClick={() => setViewMode('add')}
              className="flex items-center gap-1 px-3 py-1 rounded-[8px] text-[12px] font-medium text-[#22d3ee] bg-[#0c2a38]/60 border border-[#196b7d]/60 hover:bg-[#0c2a38] hover:border-[#22d3ee]/80 transition-all active:scale-95"
            >
              <Plus className="w-3.5 h-3.5" />
              <span>Thêm điểm nối</span>
            </button>
          </div>

          {/* Profile List */}
          <div id="popover-profile-list" className="px-3.5 space-y-2.5 max-h-[340px] overflow-y-visible">
            {profiles.map((profile) => {
              const isConnecting = (connectionState === 'connecting' && activeProfileId === profile.id) || (profile.isEnabled && connectionState === 'connecting');
              const isConnected = connectionState === 'connected' && profile.isEnabled;
              const isReconnecting = !!profile.isReconnecting || (connectionState === 'reconnecting' && activeProfileId === profile.id);
              const isMenuOpen = activeMenuId === profile.id;

              return (
                <div
                  key={profile.id}
                  id={`profile-card-${profile.id}`}
                  onClick={() => onToggleProfile(profile)}
                  className={`relative rounded-[14px] transition-all duration-200 cursor-pointer ${
                    isMenuOpen ? 'z-40' : 'z-10'
                  } ${
                    isReconnecting
                      ? 'shadow-[0_0_22px_rgba(239,68,68,0.45)] border border-red-500/80'
                      : isConnecting
                      ? 'shadow-[0_0_22px_rgba(245,158,11,0.45)] border border-amber-500/80'
                      : isConnected
                      ? 'shadow-[0_0_22px_rgba(16,185,129,0.45)] border border-[#10b981]/80'
                      : 'bg-[#181f2b]/70 border border-white/[0.08] hover:border-white/20 hover:bg-[#181f2b]'
                  }`}
                >
                  {/* Animated Rotating Linear Light Border on Connecting (Orange), Active (Green), or Reconnecting (Red) State */}
                  {(isConnected || isConnecting || isReconnecting) && (
                    <div className="absolute inset-0 rounded-[14px] pointer-events-none overflow-hidden z-0">
                      <div
                        className={`absolute -inset-[150%] animate-spin-slow ${
                          isReconnecting
                            ? 'bg-[conic-gradient(from_0deg,transparent_0_300deg,#ef4444_330deg,#fca5a5_360deg)]'
                            : isConnecting
                            ? 'bg-[conic-gradient(from_0deg,transparent_0_300deg,#f59e0b_330deg,#fde68a_360deg)]'
                            : 'bg-[conic-gradient(from_0deg,transparent_0_300deg,#10b981_330deg,#6ee7b7_360deg)]'
                        }`}
                      />
                      <div
                        className={`absolute inset-[1.5px] rounded-[13px] ${
                          isReconnecting ? 'bg-[#220a10]/95' : isConnecting ? 'bg-[#241306]/95' : 'bg-[#06241a]/95'
                        }`}
                      />
                    </div>
                  )}

                  {/* Card Inner Content */}
                  <div className="relative z-10 flex items-center justify-between px-4 py-3.5">
                    {/* Left Info */}
                    <div className="flex items-center gap-3 min-w-0 pr-2">
                      <div className="relative flex items-center justify-center flex-shrink-0">
                        <div
                          className={`w-2.5 h-2.5 rounded-full transition-all duration-300 ${
                            isReconnecting
                              ? 'bg-[#ef4444] shadow-[0_0_10px_#ef4444] animate-pulse'
                              : isConnecting
                              ? 'bg-amber-400 shadow-[0_0_10px_#f59e0b] animate-pulse'
                              : isConnected
                              ? 'bg-[#10b981] shadow-[0_0_8px_#10b981]'
                              : 'bg-[#64748b]'
                          }`}
                        />
                        {(isReconnecting || isConnecting) && (
                          <span className={`absolute w-4 h-4 rounded-full animate-ping ${isReconnecting ? 'bg-red-500/30' : 'bg-amber-500/30'}`} />
                        )}
                      </div>

                      <div className="min-w-0">
                        <div
                          className={`text-[14px] font-medium tracking-tight truncate ${
                            isReconnecting
                              ? 'text-[#fecaca]'
                              : isConnecting
                              ? 'text-amber-200'
                              : isConnected
                              ? 'text-white'
                              : 'text-[#cbd5e1]'
                          }`}
                        >
                          {profile.name}
                        </div>
                        {isReconnecting ? (
                          <div className="text-[11px] text-red-300 font-medium tracking-tight flex items-center gap-1 mt-0.5">
                            <span className="w-1.5 h-1.5 rounded-full bg-red-400 animate-ping" />
                            <span>Đang thử kết nối lại...</span>
                          </div>
                        ) : isConnecting ? (
                          <div className="text-[11px] text-amber-300 font-medium tracking-tight flex items-center gap-1 mt-0.5">
                            <span className="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse" />
                            <span>Đang kết nối đến máy chủ...</span>
                          </div>
                        ) : (
                          <div className="text-[11px] text-slate-400 truncate mt-0.5">
                            {profile.serverAddress}
                          </div>
                        )}
                      </div>
                    </div>

                    {/* Right Controls: Switch Toggle & Context Menu */}
                    <div className="flex items-center gap-2.5 flex-shrink-0" onClick={(e) => e.stopPropagation()}>
                      {/* Switch Toggle */}
                      <button
                        type="button"
                        id={`toggle-switch-${profile.id}`}
                        onClick={() => onToggleProfile(profile)}
                        className={`relative inline-flex h-[24px] w-[44px] items-center rounded-full transition-colors duration-200 focus:outline-none ${
                          isReconnecting
                            ? 'bg-[#ef4444] shadow-[0_0_12px_rgba(239,68,68,0.5)]'
                            : isConnecting
                            ? 'bg-amber-500 shadow-[0_0_12px_rgba(245,158,11,0.5)]'
                            : isConnected
                            ? 'bg-[#10b981] shadow-[0_0_12px_rgba(16,185,129,0.4)]'
                            : 'bg-[#334155]/80 hover:bg-[#475569]'
                        }`}
                      >
                        <span
                          className={`inline-block h-[18px] w-[18px] transform rounded-full bg-white transition-transform duration-200 shadow-md ${
                            isConnected || isConnecting || isReconnecting ? 'translate-x-[22px]' : 'translate-x-[3px]'
                          }`}
                        />
                      </button>

                      {/* Context Menu Button */}
                      <div className="relative">
                        <button
                          id={`btn-menu-${profile.id}`}
                          onClick={(e) => {
                            e.stopPropagation();
                            setActiveMenuId(activeMenuId === profile.id ? null : profile.id);
                          }}
                          className={`p-1.5 rounded-lg transition-colors ${
                            isMenuOpen ? 'bg-white/20 text-white' : 'text-[#8e9aa8] hover:text-white hover:bg-white/10'
                          }`}
                        >
                          <MoreHorizontal className="w-4 h-4" />
                        </button>

                        {/* Dropdown Menu - Positioned with high z-index and NOT clipped */}
                        {isMenuOpen && (
                          <div
                            id={`dropdown-menu-${profile.id}`}
                            className="absolute right-0 top-9 w-44 rounded-xl bg-[#1a212d] border border-white/15 shadow-[0_12px_30px_rgba(0,0,0,0.85)] py-1.5 z-50 text-[13px] text-slate-200 animate-in fade-in zoom-in-95 duration-100"
                          >
                            <button
                              id={`btn-edit-profile-${profile.id}`}
                              onClick={(e) => {
                                e.stopPropagation();
                                handleStartEdit(profile);
                              }}
                              className="w-full text-left px-3 py-1.5 hover:bg-white/10 flex items-center gap-2 cursor-pointer"
                            >
                              <Edit3 className="w-3.5 h-3.5 text-blue-400" />
                              <span>Chỉnh sửa hồ sơ</span>
                            </button>

                            <div className="my-1 border-t border-white/10" />

                            <button
                              id={`btn-delete-profile-${profile.id}`}
                              onClick={(e) => {
                                e.stopPropagation();
                                onDeleteProfile(profile.id);
                                setActiveMenuId(null);
                              }}
                              className="w-full text-left px-3 py-1.5 hover:bg-red-500/20 text-red-400 flex items-center gap-2 cursor-pointer"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                              <span>Xóa hồ sơ</span>
                            </button>
                          </div>
                        )}
                      </div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Bottom Section: Clean Info & Quit button */}
          <div id="popover-bottom-controls" className="px-4 py-2.5 border-t border-white/[0.08] mt-3 flex items-center justify-between">
            <div className="flex items-center gap-2 text-[11.5px] text-[#8e9aa8]">
              <span className="w-1.5 h-1.5 rounded-full bg-cyan-400" />
              <span className="font-medium">TMS-VPN Client v1.0</span>
            </div>

            <button
              id="btn-quit-tms-vpn"
              onClick={onQuitApp}
              className="flex items-center gap-1.5 text-[12px] text-[#8e9aa8] hover:text-white transition-colors group py-1 px-2 rounded-lg hover:bg-white/5"
            >
              <LogOut className="w-3.5 h-3.5 group-hover:-translate-x-0.5 transition-transform" />
              <span className="font-medium">Thoát</span>
              <kbd className="text-[10.5px] font-sans px-1.5 py-0.5 rounded bg-white/5 text-[#8e9aa8] group-hover:text-white border border-white/10 ml-0.5">
                ⌘Q
              </kbd>
            </button>
          </div>
        </div>
      )}
    </div>
  );
};
