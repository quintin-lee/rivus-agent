import { describe, it, expect, beforeEach } from 'vitest'
import { t, setI18nLangForTest } from '@/i18n'
import { useUiStore } from '@/store/ui-store'

describe('i18n t()', () => {
  beforeEach(() => {
    localStorage.clear()
    useUiStore.setState({ lang: 'zh' })
  })

  it('returns zh string under default lang', () => {
    expect(t('common.cancel')).toBe('取消')
  })

  it('returns en string after lang switch', () => {
    useUiStore.setState({ lang: 'en' })
    expect(t('common.cancel')).toBe('Cancel')
  })

  it('substitutes {name} params', () => {
    expect(t('sessionlist.deleteConfirmBody', { count: 3 })).toContain('3 个 Run')
  })

  it('falls back to zh when en key missing', () => {
    setI18nLangForTest('en')
    expect(t('i18n.test.zhonly')).toBe('仅中文')
  })

  it('returns the key itself when unknown', () => {
    expect(t('no.such.key')).toBe('no.such.key')
  })
})
