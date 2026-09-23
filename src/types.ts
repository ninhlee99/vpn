export type ConnectionState = 'connected' | 'connecting' | 'reconnecting' | 'disconnecting' | 'disconnected' | 'error';

export interface VPNProfile {
  id: string;
  name: string;
  serverAddress: string;
  username?: string;
  password?: string;
  sharedSecret?: string; // L2TP Pre-shared Key
  sendAllTraffic?: boolean;
  isEnabled: boolean;
  isReconnecting?: boolean;
  latencyMs?: number;
}

export interface AppSettings {
  autoConnectOnLaunch: boolean;
  startAtLogin: boolean;
  killSwitchEnabled: boolean;
}


export interface SwiftSourceFile {
  filename: string;
  path: string;
  description: string;
  category: 'core' | 'views' | 'backend' | 'build';
  content: string;
}

