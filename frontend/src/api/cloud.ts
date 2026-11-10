import axios from 'axios';
import { appPath } from '@/utils/appPath';

export type CloudProvider = 'baidu' | 'quark' | 'xunlei';
export type CloudMode = 'transfer' | 'subscribe';
export interface CloudTarget { provider: CloudProvider; title: string; url: string; password?: string; mode: CloudMode }
export interface CredentialField { key: string; label: string; help: string; optional?: boolean }
export interface ProviderInfo {
  id: CloudProvider; name: string; root_id: string; website: string; help: string;
  qr_login?: boolean;
  credential_fields: CredentialField[];
}
export interface CloudQR { id: string; provider: CloudProvider; status: string; message: string; image?: string; qr_url?: string; expires_at: number; account?: CloudAccount }
export interface CloudAccount { id: string; provider: CloudProvider; name: string; status: string; last_error: string; created_at: number; updated_at: number }
export interface CloudFolder { id: string; name: string }
export interface TransferRequest {
  filter_ads?: boolean;
  account_id: string; title: string; share_url: string; password: string;
  destination_id: string; destination_name: string; request_id: string;
}
export interface ContentProgress { kind: 'episode' | 'issue' | 'date' | 'unknown'; value?: string; label?: string; evidence?: string }
export interface FollowProgress { source: ContentProgress; saved: ContentProgress; checked_at: number; confirmed_files: number }
export interface CloudSubscription extends TransferRequest {
  progress?: FollowProgress;
  id: string; provider: CloudProvider; interval_minutes: number; enabled: boolean; status: string;
  last_checked_at: number; next_check_at: number; last_error: string; transferred: number; created_at: number;
}
export interface CloudJob {
  progress?: FollowProgress;
  id: string; account_id: string; subscription_id: string; provider: CloudProvider; title: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'partial' | 'interrupted';
  saved: number; skipped: number; filtered?: number; total: number; error: string; created_at: number; finished_at: number;
}

const cloudApi = axios.create({ baseURL: appPath('/api/cloud'), timeout: 120000 });
cloudApi.interceptors.request.use(config => {
  if (!['get', 'head'].includes((config.method || 'get').toLowerCase())) {
    config.headers.set('X-Seekbox-Action', '1');
    config.headers.set('Content-Type', 'application/json');
  }
  return config;
});

// Never expose Axios request/config objects: they can contain account credentials.
export const cloudError = (error: unknown): string => {
  if (axios.isAxiosError(error)) {
    const message = error.response?.data?.error;
    if (typeof message === 'string' && message) return message;
    if (error.response?.status === 401) return '登录状态已失效，请刷新页面后重试';
    if (error.code === 'ECONNABORTED') return '请求超时，请刷新状态确认结果后再重试';
    return '无法连接网盘服务，请稍后重试';
  }
  return '操作失败，请稍后重试';
};
export const cloudRequestRejected = (error: unknown) => axios.isAxiosError(error) && error.response?.status === 400;

export const supportedCloudProvider = (value: string): value is CloudProvider => ['baidu', 'quark', 'xunlei'].includes(value);
export const cloudJobActive = (job: CloudJob) => ['queued', 'running'].includes(job.status);
export const cloudJobStatus = (status: CloudJob['status']) => ({ queued: '等待执行', running: '正在转存', succeeded: '转存完成', failed: '转存失败', partial: '部分转存完成', interrupted: '任务中断' }[status] || status);
export const cloudTime = (value: number) => value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '尚未检查';
export const cloudRequestID = () => {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('');
};

export const listCloudProviders = async () => (await cloudApi.get<{ providers: ProviderInfo[] }>('/providers')).data.providers || [];
export const startCloudQR = async (body: { provider: CloudProvider; account_id?: string; name: string }) => (await cloudApi.post<{ session: CloudQR }>('/qr', body)).data.session;
export const getCloudQR = async (id: string) => (await cloudApi.get<{ session: CloudQR }>(`/qr/${encodeURIComponent(id)}`)).data.session;
export const completeCloudQR = async (id: string, name: string) => (await cloudApi.post<{ session: CloudQR }>(`/qr/${encodeURIComponent(id)}/complete`, { name })).data.session;
export const cancelCloudQR = async (id: string) => { await cloudApi.delete(`/qr/${encodeURIComponent(id)}`); };
export const listCloudAccounts = async () => (await cloudApi.get<{ accounts: CloudAccount[] }>('/accounts')).data.accounts || [];
export const connectCloudAccount = async (body: { provider: CloudProvider; name: string; credentials: Record<string, string> }, id?: string) =>
  (id ? await cloudApi.put<{ account: CloudAccount }>(`/accounts/${encodeURIComponent(id)}`, body) : await cloudApi.post<{ account: CloudAccount }>('/accounts', body)).data.account;
export const checkCloudAccount = async (id: string) => (await cloudApi.post<{ check: FunctionCheck }>(`/accounts/${encodeURIComponent(id)}/function-check`, {})).data.check;
export const deleteCloudAccount = async (id: string) => { await cloudApi.delete(`/accounts/${encodeURIComponent(id)}`); };
export const listCloudFolders = async (id: string, parent: string) => (await cloudApi.get<{ folders: CloudFolder[] }>(`/accounts/${encodeURIComponent(id)}/folders`, { params: { parent } })).data.folders || [];
export const createCloudTransfer = async (body: TransferRequest) => (await cloudApi.post<{ job: CloudJob }>('/transfers', body)).data.job;
export const listCloudJobs = async () => (await cloudApi.get<{ jobs: CloudJob[] }>('/jobs')).data.jobs || [];
export const getCloudJob = async (id: string) => (await cloudApi.get<{ job: CloudJob }>(`/jobs/${encodeURIComponent(id)}`)).data.job;
export const listCloudSubscriptions = async () => (await cloudApi.get<{ subscriptions: CloudSubscription[] }>('/subscriptions')).data.subscriptions || [];
export const createCloudSubscription = async (body: TransferRequest & { interval_minutes: number }) => (await cloudApi.post<{ subscription: CloudSubscription; job: CloudJob }>('/subscriptions', body)).data;
export const updateCloudSubscription = async (id: string, body: { enabled?: boolean; interval_minutes?: number; filter_ads?: boolean }) => (await cloudApi.patch<{ subscription: CloudSubscription }>(`/subscriptions/${encodeURIComponent(id)}`, body)).data.subscription;
export const checkCloudSubscription = async (id: string) => (await cloudApi.post<{ job: CloudJob }>(`/subscriptions/${encodeURIComponent(id)}/check`, {})).data.job;
export const deleteCloudSubscription = async (id: string) => { await cloudApi.delete(`/subscriptions/${encodeURIComponent(id)}`); };

export interface CheckStage { stage: string; status: string; duration_ms: number; error_code?: string }
export interface FunctionCheck { id: string; account_id: string; provider: string; status: string; trigger: string; created_at: number; finished_at: number; scheduled_for: number; stages: CheckStage[]; error: string; cleanup_pending: boolean; target_name?: string; cleanup_status?: string }
export interface CenterSite { id: string; name: string; url: string; enabled: boolean; order: number }
export interface CenterStatus { config: { daily_enabled: boolean; reporting_enabled: boolean; enabled_since: number }; connected: boolean; pending_events: number; last_report_at: number; last_error: string; rejected_events: number; report_error: string; sites?: CenterSite[]; sites_updated_at: number }
export const getCenterStatus = async () => (await cloudApi.get<CenterStatus>('/center')).data;
export const setCenterConfig = async (patch: { daily_enabled?: boolean; reporting_enabled?: boolean }) => (await cloudApi.put<CenterStatus>('/center', patch)).data;
export const listFunctionChecks = async () => (await cloudApi.get<{ checks: FunctionCheck[] }>('/function-checks')).data.checks || [];
export const cleanupFunctionCheck = async (id: string) => { await cloudApi.post(`/function-checks/${encodeURIComponent(id)}/cleanup`, {}); };
export const fetchCenterSites = async () => (await cloudApi.get<{ sites: CenterSite[]; stale?: boolean }>('/center/sites')).data;
