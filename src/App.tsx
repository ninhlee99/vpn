import React, { useState, useEffect } from 'react';
import { MacOSMenuBar } from './components/MacOSMenuBar';
import { TMSVPNPopover, PopoverViewMode } from './components/TMSVPNPopover';
import { SwiftSourceViewer } from './components/SwiftSourceViewer';
import { INITIAL_PROFILES, INITIAL_SETTINGS } from './data/initialData';
import { VPNProfile, ConnectionState, AppSettings } from './types';
import { Code2, X } from 'lucide-react';

export default function App() {
  const [profiles, setProfiles] = useState<VPNProfile[]>(INITIAL_PROFILES);
  const [settings, setSettings] = useState<AppSettings>(INITIAL_SETTINGS);
  const [isPopoverOpen, setIsPopoverOpen] = useState<boolean>(true);
  const [popoverViewMode, setPopoverViewMode] = useState<PopoverViewMode>('list');
  const [connectionState, setConnectionState] = useState<ConnectionState>('connected');
  const [activeProfileId, setActiveProfileId] = useState<string | null>('prod-hq');

  // Swift code viewer modal
  const [isCodeModalOpen, setIsCodeModalOpen] = useState<boolean>(false);

  // Dynamic Anchor calculations for Popover & its Arrow pointing directly to Status Bar Icon
  const [popoverPos, setPopoverPos] = useState<{
    top: number;
    left: number;
    arrowLeft: number;
  }>({
    top: 36,
    left: typeof window !== 'undefined' ? Math.max(12, window.innerWidth - 392) : 100,
    arrowLeft: 300
  });

  const updatePopoverPosition = () => {
    const trigger = document.getElementById('menubar-tms-vpn-trigger');
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const triggerCenterX = rect.left + rect.width / 2;
    const popoverWidth = Math.min(380, window.innerWidth - 24);

    let left = triggerCenterX - popoverWidth / 2;
    const minLeft = 12;
    const maxLeft = window.innerWidth - popoverWidth - 12;
    left = Math.max(minLeft, Math.min(maxLeft, left));

    const arrowLeft = Math.max(20, Math.min(popoverWidth - 20, triggerCenterX - left));

    setPopoverPos({
      top: rect.bottom + 12,
      left,
      arrowLeft
    });
  };

  useEffect(() => {
    if (isPopoverOpen) {
      updatePopoverPosition();
      const timer = setTimeout(updatePopoverPosition, 50);
      window.addEventListener('resize', updatePopoverPosition);
      window.addEventListener('scroll', updatePopoverPosition);
      return () => {
        clearTimeout(timer);
        window.removeEventListener('resize', updatePopoverPosition);
        window.removeEventListener('scroll', updatePopoverPosition);
      };
    }
  }, [isPopoverOpen, connectionState, activeProfileId, popoverViewMode]);

  // Handle profile toggle
  const handleToggleProfile = (profile: VPNProfile) => {
    const wasActive = profile.isEnabled;

    if (wasActive) {
      setProfiles((prev) =>
        prev.map((p) => (p.id === profile.id ? { ...p, isEnabled: false } : p))
      );
      setConnectionState('disconnected');
      setActiveProfileId(null);
    } else {
      setConnectionState('connecting');
      setProfiles((prev) =>
        prev.map((p) => ({
          ...p,
          isEnabled: p.id === profile.id
        }))
      );
      setActiveProfileId(profile.id);

      // Realistic 2.2s connection loop to show the rotating 2.0s border animation
      setTimeout(() => {
        setConnectionState('connected');
      }, 2200);
    }
  };

  const handleAddProfile = (newProfileData: Omit<VPNProfile, 'id' | 'latencyMs'>) => {
    const newProfile: VPNProfile = {
      ...newProfileData,
      id: `profile-${Date.now()}`,
      latencyMs: Math.floor(Math.random() * 30) + 15
    };
    setProfiles((prev) => [...prev, newProfile]);
  };

  const handleSaveProfile = (updated: VPNProfile) => {
    setProfiles((prev) => prev.map((p) => (p.id === updated.id ? updated : p)));
  };

  const handleDeleteProfile = (id: string) => {
    setProfiles((prev) => prev.filter((p) => p.id !== id));
    if (activeProfileId === id) {
      setActiveProfileId(null);
      setConnectionState('disconnected');
    }
  };

  const handleUpdateSettings = (partial: Partial<AppSettings>) => {
    setSettings((prev) => ({ ...prev, ...partial }));
  };

  const handleToggleReconnecting = (id: string) => {
    setProfiles((prev) =>
      prev.map((p) => {
        if (p.id === id) {
          const nextReconnecting = !p.isReconnecting;
          setConnectionState(nextReconnecting ? 'reconnecting' : 'connected');
          return { ...p, isReconnecting: nextReconnecting };
        }
        return p;
      })
    );
  };

  const activeProfile = profiles.find((p) => p.isEnabled) || null;

  return (
    <div className="min-h-screen bg-[#07090e] text-slate-100 flex flex-col font-sans select-none overflow-hidden relative">
      {/* Native macOS Top Menu Bar */}
      <MacOSMenuBar
        isPopoverOpen={isPopoverOpen}
        onTogglePopover={() => {
          if (isPopoverOpen) {
            setIsPopoverOpen(false);
          } else {
            setIsPopoverOpen(true);
            setPopoverViewMode('list');
          }
        }}
        connectionState={connectionState}
        activeProfileName={activeProfile?.name || null}
        onOpenAddModal={() => {
          setIsPopoverOpen(true);
          setPopoverViewMode('add');
        }}
        onQuitApp={() => {
          setConnectionState('disconnected');
          setIsPopoverOpen(false);
        }}
      />

      {/* Main Desktop Canvas */}
      <div
        className="flex-1 w-full relative flex items-start justify-end p-4 sm:p-8 overflow-hidden bg-gradient-to-br from-[#0c131d] via-[#09151c] to-[#05080e]"
        onClick={() => {
          // Clicking on wallpaper background does not interfere
        }}
      >
        {/* Subtle macOS wallpaper ambient glow */}
        <div className="absolute top-12 right-24 w-72 h-72 rounded-full bg-cyan-500/10 blur-3xl pointer-events-none" />
        <div className="absolute bottom-16 left-16 w-80 h-80 rounded-full bg-emerald-500/10 blur-3xl pointer-events-none" />

        {/* Popover container positioned right under the topbar status icon with exact arrow alignment */}
        {isPopoverOpen && (
          <div
            className="fixed z-40 transition-all duration-100 ease-out"
            style={{
              top: `${popoverPos.top}px`,
              left: `${popoverPos.left}px`
            }}
          >
            <TMSVPNPopover
              profiles={profiles}
              activeProfileId={activeProfileId}
              connectionState={connectionState}
              settings={settings}
              viewMode={popoverViewMode}
              arrowLeft={popoverPos.arrowLeft}
              onViewModeChange={setPopoverViewMode}
              onClosePopover={() => setIsPopoverOpen(false)}
              onToggleProfile={handleToggleProfile}
              onAddProfile={handleAddProfile}
              onSaveProfile={handleSaveProfile}
              onDeleteProfile={handleDeleteProfile}
              onUpdateSettings={handleUpdateSettings}
              onToggleReconnecting={handleToggleReconnecting}
              onQuitApp={() => {
                setConnectionState('disconnected');
                setIsPopoverOpen(false);
              }}
            />
          </div>
        )}

        {/* Minimalist Bottom-Left Quick Trigger for Swift Single Binary Code */}
        <div className="absolute bottom-4 left-4 z-20 flex items-center gap-3">
          <button
            id="btn-open-swift-code"
            onClick={() => setIsCodeModalOpen(true)}
            className="flex items-center gap-2 px-3.5 py-2 rounded-xl bg-[#12161f]/90 hover:bg-[#181f2c] border border-white/10 text-slate-300 hover:text-white text-[12px] font-medium shadow-lg backdrop-blur-md transition-all active:scale-95"
          >
            <Code2 className="w-4 h-4 text-cyan-400" />
            <span>View Swift source (single binary)</span>
          </button>
        </div>

        {/* Live Interactive Animation State Switcher */}
        <div className="absolute bottom-4 right-4 z-20 hidden md:flex items-center gap-1.5 p-1.5 rounded-xl bg-[#12161f]/95 border border-white/10 shadow-2xl backdrop-blur-md text-[11px]">
          <span className="text-slate-400 px-2 font-medium">Simulate state:</span>
          <button
            onClick={() => {
              setConnectionState('connecting');
              setIsPopoverOpen(true);
            }}
            className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
              connectionState === 'connecting'
                ? 'bg-amber-500/20 text-amber-300 border border-amber-500/40 shadow-[0_0_10px_rgba(245,158,11,0.3)]'
                : 'text-slate-400 hover:text-white hover:bg-white/5'
            }`}
          >
            🟠 Connecting
          </button>
          <button
            onClick={() => {
              setConnectionState('connected');
              setIsPopoverOpen(true);
            }}
            className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
              connectionState === 'connected'
                ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/40 shadow-[0_0_10px_rgba(16,185,129,0.3)]'
                : 'text-slate-400 hover:text-white hover:bg-white/5'
            }`}
          >
            🟢 Connected
          </button>
          <button
            onClick={() => {
              setConnectionState('reconnecting');
              setIsPopoverOpen(true);
            }}
            className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
              connectionState === 'reconnecting'
                ? 'bg-red-500/20 text-red-300 border border-red-500/40 shadow-[0_0_10px_rgba(239,68,68,0.3)]'
                : 'text-slate-400 hover:text-white hover:bg-white/5'
            }`}
          >
            🔴 Reconnecting
          </button>
          <button
            onClick={() => {
              setConnectionState('disconnected');
              setIsPopoverOpen(true);
            }}
            className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
              connectionState === 'disconnected'
                ? 'bg-slate-700/50 text-slate-200 border border-slate-600'
                : 'text-slate-400 hover:text-white hover:bg-white/5'
            }`}
          >
            ⚪ Disconnected
          </button>
        </div>
      </div>

      {/* Swift Source Code Modal */}
      {isCodeModalOpen && (
        <div
          className="fixed inset-0 bg-black/75 backdrop-blur-md flex items-center justify-center z-50 p-4 animate-in fade-in duration-150"
          onClick={() => setIsCodeModalOpen(false)}
        >
          <div
            onClick={(e) => e.stopPropagation()}
            className="w-full max-w-4xl max-h-[85vh] bg-[#10141d] border border-white/15 rounded-2xl shadow-2xl overflow-hidden flex flex-col"
          >
            <div className="px-5 py-3.5 border-b border-white/10 flex items-center justify-between bg-[#141a26]">
              <div className="flex items-center gap-2">
                <Code2 className="w-4 h-4 text-cyan-400" />
                <span className="text-[14px] font-bold text-white">Native Swift Source (single standalone binary, zero dependencies)</span>
              </div>
              <button
                onClick={() => setIsCodeModalOpen(false)}
                className="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-white/10"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-4 overflow-y-auto flex-1">
              <SwiftSourceViewer />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
