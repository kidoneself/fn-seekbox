import axios from 'axios';
import { appPath } from '@/utils/appPath';

export interface UpdateRelease { version: string; notes: string; size: number; arch: string }
export interface UpdateStatus {
  enabled: boolean; auto_enabled: boolean; can_manage: boolean;
  current_version: string; bootstrap_version?: number; phase: string;
  error?: string; available: boolean; release?: UpdateRelease;
  last_checked_at?: number | string; updated_at?: number | string;
}
const api = axios.create({ baseURL: appPath('/api/update'), timeout: 15000 });
export const getUpdateStatus = async () => (await api.get<UpdateStatus>('/status')).data;
export const runUpdateAction = async (action: 'check' | 'install') =>
  (await api.post<UpdateStatus>(`/${action}`, {}, { headers: { 'X-Seekbox-Action': '1' } })).data;
export const setAutoUpdate = async (auto: boolean) =>
  (await api.patch<UpdateStatus>('/settings', { auto_update: auto }, { headers: { 'X-Seekbox-Action': '1' } })).data;
export function updateError(error: unknown): string {
  if (axios.isAxiosError(error)) {
    if (error.response?.status === 403) return '当前账号没有更新管理权限';
    if (error.response?.status === 401) return '登录已失效，请刷新页面';
    if (typeof error.response?.data?.error === 'string') return error.response.data.error;
  }
  return '暂时无法连接更新服务，正在等待恢复';
}
