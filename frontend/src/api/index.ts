import axios from 'axios';
import type { SearchResponse, LinkCheckItem, LinkCheckResponse } from '@/types';
import { appPath } from '@/utils/appPath';

const api = axios.create({
  baseURL: appPath('/api'),
  timeout: 10000
});

// 访问控制统一交给 fnOS 网关。清掉旧版独立登录残留，避免把过期令牌
// 作为 Authorization 转给后端后出现 “invalid token”。
localStorage.removeItem('auth_token');
localStorage.removeItem('auth_username');

// 搜索参数接口
export interface SearchParams {
  kw: string;
  refresh?: boolean;
  res?: 'all' | 'results' | 'merge';
  src?: 'all' | 'tg' | 'plugin';
  plugins?: string;
  channels?: string;
  cloud_types?: string;
  ext?: string;
}

// API响应包装类型
interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

// 健康状态接口（基于实际API返回）
export interface HealthStatus {
  version?: string;
  bootstrap_version?: number;
  status: string;
  plugins_enabled: boolean;
  plugin_count: number;
  plugins: string[];
  channels: string[];
  auth_enabled?: boolean;
}

// 登录请求参数
export interface LoginParams {
  username: string;
  password: string;
}

// 登录响应
export interface LoginResponse {
  token: string;
  expires_at: number;
  username: string;
}

// 认证状态
export interface AuthStatus {
  enabled: boolean;
  authenticated: boolean;
}

// 获取API健康状态
export const getHealth = async (): Promise<HealthStatus> => {
  const response = await api.get<HealthStatus>('/health');
  return response.data;
};

// 搜索API
export const search = async (params: SearchParams, signal?: AbortSignal): Promise<SearchResponse> => {
  // 添加ext参数，包含referer信息
  const searchParams = {
    ...params,
    ext: JSON.stringify({ referer: "https://dm.xueximeng.com" })
  };
  
  const response = await api.get<ApiResponse<SearchResponse>>('/search', {
    params: searchParams,
    signal,
    timeout: 180000
  });
  
  // 如果响应中包含data字段，则返回data
  if (response.data && response.data.data) {
    return response.data.data;
  }
  
  // 如果响应本身就是SearchResponse格式
  if (response.data && response.data.total !== undefined && response.data.merged_by_type) {
    return response.data as unknown as SearchResponse;
  }
  
  // 返回空结果
  return {
    total: 0,
    results: [],
    merged_by_type: {}
  };
};

// 登录
export const login = async (params: LoginParams): Promise<LoginResponse> => {
  const response = await api.post<LoginResponse>('/auth/login', params);
  return response.data;
};

// 验证token
export const verifyToken = async (): Promise<boolean> => {
  try {
    await api.post('/auth/verify');
    return true;
  } catch {
    return false;
  }
};

// 退出登录
export const logout = async (): Promise<void> => {
  try {
    await api.post('/auth/logout');
  } finally {
    localStorage.removeItem('auth_token');
    localStorage.removeItem('auth_username');
  }
};

export const inspectVisibleLinks = async (
  items: LinkCheckItem[],
  view_token?: string
): Promise<LinkCheckResponse> => {
  const response = await api.post<LinkCheckResponse>('/check/links', {
    items,
    view_token
  }, { timeout: 120000 });
  return response.data;
};

// 检查认证状态
export const checkAuthStatus = async (): Promise<AuthStatus> => {
  try {
    const health = await getHealth();
    const authEnabled = health.auth_enabled || false;
    const token = localStorage.getItem('auth_token');
    
    if (!authEnabled) {
      return { enabled: false, authenticated: true };
    }
    
    if (!token) {
      return { enabled: true, authenticated: false };
    }
    
    const valid = await verifyToken();
    return { enabled: true, authenticated: valid };
  } catch {
    return { enabled: false, authenticated: true };
  }
};

export default api; 
