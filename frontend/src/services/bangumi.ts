// bangumi (bgm.tv) 元数据检索服务封装层
//
// 对 bindings/tg-download/services 的 BangumiService 二次封装。
// 类型直接由视图层从 bindings 导入，本文件仅封装方法调用。

import type { BangumiBrowseRequest } from '../../bindings/tg-download/internal/api/models.js'
import type {
  BangumiPagedEpisode,
  BangumiPagedSubject,
  BangumiSubject,
} from '../../bindings/tg-download/internal/api/models.js'

// bindings 文件生成后从此路径导入；生成前调用会报错，属预期行为
let BangumiServiceBinding: any = null

async function loadBinding() {
  if (!BangumiServiceBinding) {
    const mod: any = await import('../../bindings/tg-download/services/index')
    BangumiServiceBinding = mod.BangumiService
  }
  return BangumiServiceBinding
}

// BrowseSubjects 按类型/年月浏览条目（GET /v0/subjects）
// token 为 bangumi access token，空则匿名访问（有速率限制，看不到 NSFW）
// userAgent 为 User-Agent（必填，格式 用户名/应用名），由前端透传到后端
export async function browseSubjects(
  req: BangumiBrowseRequest,
  token: string,
  userAgent: string
): Promise<BangumiPagedSubject> {
  const svc = await loadBinding()
  return svc.BrowseSubjects(req, token, userAgent)
}

// GetSubject 获取条目详情（GET /v0/subjects/{id}）
export async function getSubject(
  subjectId: number,
  token: string,
  userAgent: string
): Promise<BangumiSubject> {
  const svc = await loadBinding()
  return svc.GetSubject(subjectId, token, userAgent)
}

// GetEpisodes 获取条目章节列表（GET /v0/episodes）
// epType: 0=本篇 1=SP 2=OP 3=ED 4=PV 5=MAD 6=其他；传 -1 表示不按类型筛选
export async function getEpisodes(
  subjectId: number,
  epType: number,
  limit: number,
  offset: number,
  token: string,
  userAgent: string
): Promise<BangumiPagedEpisode> {
  const svc = await loadBinding()
  return svc.GetEpisodes(subjectId, epType, limit, offset, token, userAgent)
}
