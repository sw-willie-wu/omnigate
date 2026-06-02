import { describe, expect, test } from 'vitest'
import { formatSize } from '../utils/format'

describe('formatSize', () => {
  test('zero bytes', () => {
    expect(formatSize(0)).toBe('0 MB')
  })
  test('sub-MB', () => {
    expect(formatSize(500 * 1024)).toBe('0 MB')
  })
  test('sub-GB', () => {
    expect(formatSize(500 * 1024 * 1024)).toBe('500 MB')
  })
  test('over-GB', () => {
    expect(formatSize(2.5 * 1024 * 1024 * 1024)).toBe('2.5 GB')
  })
  test('TB-class', () => {
    expect(formatSize(2 * 1024 * 1024 * 1024 * 1024)).toBe('2048.0 GB')
  })
})
