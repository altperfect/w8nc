import { describe, expect, it } from 'vitest'
import { parseCurl } from './curl'

describe('curl import', () => {
  it('imports a multiline JSON request with quoted headers', () => {
    const result = parseCurl(`curl 'https://example.com/api?q=1&b=2' \\\n      -H 'Authorization: Bearer secret' \\\n      --data-raw '{"name":"hello world"}' -H 'Content-Type: application/json'`)
    expect(result.http_method).toBe('POST')
    expect(result.request_body).toBe('{"name":"hello world"}')
    expect(result.headers?.[0]).toEqual({ name: 'Authorization', value: 'Bearer secret', sensitive: true, masked: true })
    expect(result.headers).toHaveLength(2)
  })
  it('handles explicit methods, JSON and attached option values', () => {
    expect(parseCurl('curl --url=https://example.com -XPATCH --json=42')).toMatchObject({ http_method: 'PATCH', request_body: '42' })
    expect(parseCurl('curl -I https://example.com').http_method).toBe('HEAD')
    expect(parseCurl('curl https://example.com -G --data-urlencode "q=hello world"')).toMatchObject({ url: 'https://example.com?q=hello%20world', request_body: '', http_method: 'GET' })
  })
  it('preserves shell quoted apostrophes and double quoted escapes', () => {
    expect(parseCurl(`curl https://example.com --data-raw 'it'\\''s fine'`).request_body).toBe("it's fine")
    expect(parseCurl('curl https://example.com --data-raw "a\\qb"').request_body).toBe('a\\qb')
  })
  it.each([
    'wget https://example.com', 'curl', "curl 'https://example.com", 'curl https://example.com -X',
    'curl https://example.com --data @file', 'curl https://example.com -F a=b',
    'curl https://example.com https://other.com', 'curl file:///tmp/file',
    'curl https://example.com; whoami', 'curl https://example.com -k'
  ])('rejects malformed or unsupported requests: %s', command => {
    expect(() => parseCurl(command)).toThrow()
  })
})
