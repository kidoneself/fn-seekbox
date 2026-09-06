import axios from 'axios'
import type {
  PanlianStatusResponse,
  PanlianLoginResponse,
  PanlianLogoutResponse,
  PanlianSearchResponse
} from '@/types/panlian'
import { appPath } from '@/utils/appPath'

const panlianApi = axios.create({
  baseURL: appPath('/panlian'),
  timeout: 15000
})

panlianApi.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('auth_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

panlianApi.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('auth_token')
      localStorage.removeItem('auth_username')
      window.dispatchEvent(new CustomEvent('auth:required'))
    }
    return Promise.reject(error)
  }
)

export const getStatus = async (hash: string): Promise<PanlianStatusResponse> => {
  const response = await panlianApi.post<PanlianStatusResponse>(`/${hash}`, {
    action: 'get_status'
  })
  return response.data
}

export const login = async (
  hash: string,
  username: string,
  password: string
): Promise<PanlianLoginResponse> => {
  const response = await panlianApi.post<PanlianLoginResponse>(`/${hash}`, {
    action: 'login',
    username,
    password
  })
  return response.data
}

export const logout = async (hash: string): Promise<PanlianLogoutResponse> => {
  const response = await panlianApi.post<PanlianLogoutResponse>(`/${hash}`, {
    action: 'logout'
  })
  return response.data
}

export const testSearch = async (
  hash: string,
  keyword: string
): Promise<PanlianSearchResponse> => {
  const response = await panlianApi.post<PanlianSearchResponse>(`/${hash}`, {
    action: 'test_search',
    keyword
  })
  return response.data
}

export const getHashByIdentifier = async (identifier: string): Promise<string> => {
  const response = await panlianApi.get(`/${identifier}`)
  const responseUrl = response.request?.responseURL || response.config?.url || ''
  const hashMatch = responseUrl.match(/\/panlian\/([a-f0-9]{64})/)
  if (hashMatch && hashMatch[1]) {
    return hashMatch[1]
  }
  throw new Error('无法从重定向URL中提取hash')
}

export default {
  getStatus,
  login,
  logout,
  testSearch,
  getHashByIdentifier
}
