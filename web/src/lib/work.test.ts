import { describe, expect, it } from 'vitest'
import { packageLabel } from './work'

describe('packageLabel', () => {
  it('shows the package id alone when the source id repeats it', () => {
    expect(packageLabel({ packageId: 'zh.wenku8.api', sourceId: 'zh.wenku8.api' })).toBe('zh.wenku8.api')
  })

  it('adds the source id when it differs', () => {
    expect(packageLabel({ packageId: 'zh.pack', sourceId: 'site-b' })).toBe('zh.pack / site-b')
  })
})
