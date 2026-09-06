import axios from 'axios'
import type {
  WeiboStatusResponse,
  WeiboQRCodeResponse,
  WeiboLoginCheckResponse,
  WeiboLogoutResponse,
  WeiboUserIdsResponse,
  WeiboSearchResponse
} from '@/types/weibo'
import { appPath } from '@/utils/appPath'

const weiboApi = axios.create({
  baseURL: appPath('/weibo'),
  timeout: 15000
})

weiboApi.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('auth_token');
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
)

weiboApi.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('auth_token');
      localStorage.removeItem('auth_username');
      window.dispatchEvent(new CustomEvent('auth:required'));
    }
    return Promise.reject(error);
  }
)

export const getStatus = async (hash: string): Promise<WeiboStatusResponse> => {
  const response = await weiboApi.post<WeiboStatusResponse>(`/${hash}`, {
    action: 'get_status'
  })
  return response.data
}

export const refreshQRCode = async (hash: string): Promise<WeiboQRCodeResponse> => {
  const response = await weiboApi.post<WeiboQRCodeResponse>(`/${hash}`, {
    action: 'refresh_qrcode'
  })
  return response.data
}

export const checkLogin = async (hash: string): Promise<WeiboLoginCheckResponse> => {
  const response = await weiboApi.post<WeiboLoginCheckResponse>(`/${hash}`, {
    action: 'check_login'
  })
  return response.data
}

export const logout = async (hash: string): Promise<WeiboLogoutResponse> => {
  const response = await weiboApi.post<WeiboLogoutResponse>(`/${hash}`, {
    action: 'logout'
  })
  return response.data
}

export const setUserIds = async (hash: string, userIds: string[]): Promise<WeiboUserIdsResponse> => {
  const response = await weiboApi.post<WeiboUserIdsResponse>(`/${hash}`, {
    action: 'set_user_ids',
    user_ids: userIds
  })
  return response.data
}

export const testSearch = async (
  hash: string, 
  keyword: string, 
  maxResults: number = 10
): Promise<WeiboSearchResponse> => {
  const response = await weiboApi.post<WeiboSearchResponse>(`/${hash}`, {
    action: 'test_search',
    keyword,
    max_results: maxResults
  })
  return response.data
}

export const getHashByUID = async (uid: string): Promise<string> => {
  try {
    const response = await weiboApi.get(`/${uid}`)
    const responseUrl = response.request?.responseURL || response.config?.url || ''
    const hashMatch = responseUrl.match(/\/weibo\/([a-f0-9]{64})/)
    if (hashMatch && hashMatch[1]) {
      return hashMatch[1]
    }
    throw new Error('无法从重定向URL中提取hash')
  } catch (error) {
    console.error('获取hash失败:', error)
    throw error
  }
}

export default {
  getStatus,
  refreshQRCode,
  checkLogin,
  logout,
  setUserIds,
  testSearch,
  getHashByUID
}
