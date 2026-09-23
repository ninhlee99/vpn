import { VPNProfile, AppSettings } from '../types';

export const INITIAL_PROFILES: VPNProfile[] = [
  {
    id: 'prod-hq',
    name: 'Trụ sở chính (Prod)',
    serverAddress: 'vpn-prod.company.internal',
    username: 'admin@company.vn',
    password: '••••••••••••',
    sharedSecret: 'tms-corp-l2tp-secret',
    sendAllTraffic: true,
    isEnabled: true,
    isReconnecting: false,
    latencyMs: 16
  },
  {
    id: 'dev-staging',
    name: 'Môi trường Dev & Staging',
    serverAddress: 'vpn-staging.company.internal',
    username: 'developer@company.vn',
    password: '••••••••••••',
    sharedSecret: 'tms-staging-secret',
    sendAllTraffic: true,
    isEnabled: false,
    isReconnecting: false,
    latencyMs: 24
  },
  {
    id: 'branch-hcm',
    name: 'Chi nhánh TP.HCM',
    serverAddress: 'vpn-hcm.company.internal',
    username: 'user.hcm@company.vn',
    password: '••••••••••••',
    sharedSecret: 'tms-hcm-secret',
    sendAllTraffic: false,
    isEnabled: false,
    isReconnecting: false,
    latencyMs: 42
  }
];

export const INITIAL_SETTINGS: AppSettings = {
  autoConnectOnLaunch: true,
  startAtLogin: true,
  killSwitchEnabled: true
};

