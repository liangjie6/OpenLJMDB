import { api } from '@/api'
import { isApiError } from '@/api/http'

/**
 * 树内重命名使用与正文相同的保存协议：先读取正文和 revision，再提交标题变化，
 * 不绕开冲突控制（03 文档 4.1）。读取与提交之间若被其他页面更新，重新读取后再试一次。
 */
export async function renameDocument(docId: string, title: string): Promise<{ revision: number; updatedAt: number }> {
  for (let attempt = 0; attempt < 2; attempt++) {
    const latest = await api.documents.get(docId)
    if (latest.title === title) return { revision: latest.revision, updatedAt: latest.updated_at }
    try {
      const result = await api.documents.save(docId, { title, markdown: latest.markdown, expected_revision: latest.revision })
      return { revision: result.revision, updatedAt: result.updated_at }
    } catch (error) {
      if (attempt === 0 && isApiError(error) && error.code === 'REVISION_CONFLICT') continue
      throw error
    }
  }
  throw new Error('文档正在被频繁修改，请稍后重试')
}
