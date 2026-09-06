import axios from 'axios'
import type {
  QQPDStatusResponse,
  QQPDQRCodeResponse,
  QQPDLoginCheckResponse,
  QQPDLogoutResponse,
  QQPDChannelsResponse,
  QQPDSearchResponse
} from '@/types/qqpd'
import { appPath } from '@/utils/appPath'

// 创建QQPD API实例
const qqpdApi = axios.create({
  baseURL: appPath('/qqpd'),
  timeout: 15000
})

// 请求拦截器 - 自动添加token（支持安全认证）
qqpdApi.interceptors.request.use(
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

// 响应拦截器 - 处理401认证失败
qqpdApi.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      // 清除token
      localStorage.removeItem('auth_token');
      localStorage.removeItem('auth_username');
      
      // 触发显示登录窗口的事件
      window.dispatchEvent(new CustomEvent('auth:required'));
    }
    return Promise.reject(error);
  }
)

// ============================================================
// QQPD API 调用函数
// ============================================================

/**
 * 获取用户状态
 * @param hash 用户专属hash（64位）
 */
export const getStatus = async (hash: string): Promise<QQPDStatusResponse> => {
  const response = await qqpdApi.post<QQPDStatusResponse>(`/${hash}`, {
    action: 'get_status'
  })
  return response.data
}

/**
 * 刷新二维码
 * @param hash 用户专属hash
 */
export const refreshQRCode = async (hash: string): Promise<QQPDQRCodeResponse> => {
  const response = await qqpdApi.post<QQPDQRCodeResponse>(`/${hash}`, {
    action: 'refresh_qrcode'
  })
  return response.data
}

/**
 * 检查登录状态（扫码后轮询调用）
 * @param hash 用户专属hash
 */
export const checkLogin = async (hash: string): Promise<QQPDLoginCheckResponse> => {
  const response = await qqpdApi.post<QQPDLoginCheckResponse>(`/${hash}`, {
    action: 'check_login'
  })
  return response.data
}

/**
 * 退出登录
 * @param hash 用户专属hash
 */
export const logout = async (hash: string): Promise<QQPDLogoutResponse> => {
  const response = await qqpdApi.post<QQPDLogoutResponse>(`/${hash}`, {
    action: 'logout'
  })
  return response.data
}

/**
 * 设置频道列表
 * @param hash 用户专属hash
 * @param channels 频道号数组
 */
export const setChannels = async (hash: string, channels: string[]): Promise<QQPDChannelsResponse> => {
  const response = await qqpdApi.post<QQPDChannelsResponse>(`/${hash}`, {
    action: 'set_channels',
    channels
  })
  return response.data
}

/**
 * 测试搜索
 * @param hash 用户专属hash
 * @param keyword 搜索关键词
 * @param maxResults 最大返回数量（默认10）
 */
export const testSearch = async (
  hash: string, 
  keyword: string, 
  maxResults: number = 10
): Promise<QQPDSearchResponse> => {
  const response = await qqpdApi.post<QQPDSearchResponse>(`/${hash}`, {
    action: 'test_search',
    keyword,
    max_results: maxResults
  })
  return response.data
}

/**
 * 通过QQ号获取hash（请求后端重定向）
 * @param qqNumber QQ号
 */
export const getHashByQQNumber = async (qqNumber: string): Promise<string> => {
  try {
    console.log(`请求获取QQ号 ${qqNumber} 的hash值`)
    
    // 使用axios发送请求，让axios处理重定向
    const response = await qqpdApi.get(`/${qqNumber}`)
    
    // 从响应的URL中提取hash
    const responseUrl = response.request?.responseURL || response.config?.url || ''
    console.log('响应URL:', responseUrl)
    
    // 从URL中提取hash
    const hashMatch = responseUrl.match(/\/qqpd\/([a-f0-9]{64})/)
    if (hashMatch && hashMatch[1]) {
      console.log('提取到hash:', hashMatch[1])
      return hashMatch[1]
    }
    
    throw new Error('无法从重定向URL中提取hash')
  } catch (error) {
    console.error('获取hash失败:', error)
    throw error
  }
}

// 导出所有API函数
export default {
  getStatus,
  refreshQRCode,
  checkLogin,
  logout,
  setChannels,
  testSearch,
  getHashByQQNumber
}
