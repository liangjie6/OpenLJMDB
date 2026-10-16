import { describe, expect, it } from 'vitest'
import { findSourceMatches, replaceSourceMatches } from '../sourceSearch'

describe('正文源码查找和替换', () => {
  it('按字面查找 Markdown 和正则特殊字符', () => {
    const source = '# [a](b) .* $1 \\ [a](b)'
    expect(findSourceMatches(source, '[a](b)', true)).toEqual([{ start: 2, end: 8 }, { start: 17, end: 23 }])
    expect(findSourceMatches(source, '.*', true)).toEqual([{ start: 9, end: 11 }])
    expect(findSourceMatches(source, '\\', true)).toEqual([{ start: 15, end: 16 }])
    expect(findSourceMatches(source, '', false)).toEqual([])
  })

  it('支持中文、大小写切换、Unicode 原文偏移和多行查找', () => {
    expect(findSourceMatches('😀中文 Foo foo FOO', 'foo', false)).toEqual([
      { start: 5, end: 8 }, { start: 9, end: 12 }, { start: 13, end: 16 },
    ])
    expect(findSourceMatches('Foo foo FOO', 'foo', true)).toEqual([{ start: 4, end: 7 }])
    expect(findSourceMatches('İX 中文\n正文', 'X', false)).toEqual([{ start: 1, end: 2 }])
    expect(findSourceMatches('中文\n正文', '中文\n正文', true)).toEqual([{ start: 0, end: 5 }])
  })

  it('全部替换不递归匹配替换内容，空字符串可删除，美元符号保持字面内容', () => {
    const source = '**foo** foo'
    const matches = findSourceMatches(source, 'foo', true)
    expect(replaceSourceMatches(source, matches, '$&$1foo')).toBe('**$&$1foo** $&$1foo')
    expect(replaceSourceMatches(source, matches, '')).toBe('**** ')
    expect(findSourceMatches('aaa', 'aa', true)).toEqual([{ start: 0, end: 2 }])
  })
})
