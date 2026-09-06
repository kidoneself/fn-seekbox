import axios from 'axios'
import type {
  GyingConfigResponse,
  GyingStatusResponse,
  GyingLoginResponse,
  GyingLogoutResponse,
  GyingSearchResponse
} from '@/types/gying'
import { appPath } from '@/utils/appPath'

// 创建Gying API实例
const GYING_DEFAULT_TIMEOUT = 30000
const GYING_LONG_TIMEOUT = 120000

const gyingApi = axios.create({
  baseURL: appPath('/gying'),
  timeout: GYING_DEFAULT_TIMEOUT
})

// 请求拦截器 - 自动添加token（支持安全认证）
gyingApi.interceptors.request.use(
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
gyingApi.interceptors.response.use(
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
// Gying API 调用函数
// ============================================================

/**
 * 获取用户状态
 * @param hash 用户专属hash（64位）
 */
export const getStatus = async (hash: string): Promise<GyingStatusResponse> => {
  const response = await gyingApi.post<GyingStatusResponse>(`/${hash}`, {
    action: 'get_status'
  })
  return response.data
}

/**
 * 获取当前站点配置
 * @param hash 用户专属hash
 */
export const getConfig = async (hash: string): Promise<GyingConfigResponse> => {
  const response = await gyingApi.post<GyingConfigResponse>(`/${hash}`, {
    action: 'get_config'
  })
  return response.data
}

/**
 * 更新当前站点配置
 * @param hash 用户专属hash
 * @param baseURL 自定义站点地址
 */
export const updateConfig = async (hash: string, baseURL: string): Promise<GyingConfigResponse> => {
  const response = await gyingApi.post<GyingConfigResponse>(`/${hash}`, {
    action: 'update_config',
    base_url: baseURL
  })
  return response.data
}

/**
 * 登录
 * @param hash 用户专属hash
 * @param username 用户名
 * @param password 密码
 */
export const login = async (hash: string, username: string, password: string): Promise<GyingLoginResponse> => {
  const response = await gyingApi.post<GyingLoginResponse>(`/${hash}`, {
    action: 'login',
    username,
    password
  }, {
    timeout: GYING_LONG_TIMEOUT
  })
  return response.data
}

/**
 * 退出登录
 * @param hash 用户专属hash
 */
export const logout = async (hash: string): Promise<GyingLogoutResponse> => {
  const response = await gyingApi.post<GyingLogoutResponse>(`/${hash}`, {
    action: 'logout'
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
): Promise<GyingSearchResponse> => {
  const response = await gyingApi.post<GyingSearchResponse>(`/${hash}`, {
    action: 'test_search',
    keyword,
    max_results: maxResults
  }, {
    timeout: GYING_LONG_TIMEOUT
  })
  return response.data
}

/**
 * 通过用户名获取hash（请求后端重定向）
 * @param username 用户名
 */
export const getHashByUsername = async (username: string): Promise<string> => {
  try {
    console.log(`请求获取用户名 ${username} 的hash值`)
    
    // 使用axios发送请求，让axios处理重定向
    const response = await gyingApi.get(`/${username}`)
    
    // 从响应的URL中提取hash
    const responseUrl = response.request?.responseURL || response.config?.url || ''
    console.log('响应URL:', responseUrl)
    
    // 从URL中提取hash
    const hashMatch = responseUrl.match(/\/gying\/([a-f0-9]{64})/)
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
  getConfig,
  updateConfig,
  login,
  logout,
  testSearch,
  getHashByUsername
}
