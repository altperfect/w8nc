import type { EndpointInput, HeaderValue } from '../types'

// Tokenize shell quoting without executing commands or expanding variables.
function tokenize(command: string): string[] {
  const tokens: string[] = []
  let token = '', quote = '', started = false
  for (let i = 0; i < command.length; i++) {
    const char = command[i]
    if (char === '\\' && quote !== "'") {
      const next = command[++i]
      if (next === undefined) throw new Error('Incomplete escape in curl command.')
      if (next === '\n') continue
      if (quote === '"' && !['"', '\\', '$', '`'].includes(next)) token += '\\'
      token += next
      started = true
    } else if (quote) {
      if (char === quote) quote = ''
      else token += char
    } else if (char === '"' || char === "'") {
      quote = char
      started = true
    } else if (/\s/.test(char)) {
      if (started) tokens.push(token)
      token = ''; started = false
    } else {
      if (';|&<>`'.includes(char)) throw new Error('Quote URLs and values containing shell special characters. Import one curl command only.')
      token += char; started = true
    }
  }
  if (quote) throw new Error('Unclosed quote in curl command.')
  if (started) tokens.push(token)
  return tokens
}

export function parseCurl(command: string): Partial<EndpointInput> {
  const tokens = tokenize(command.replace(/\r\n/g, '\n'))
  if (tokens.shift() !== 'curl') throw new Error('Enter a command starting with curl.')
  let url = '', method = '', head = false, get = false, json = false
  const headers: HeaderValue[] = [], data: string[] = []
  const header = (name: string, value: string) => headers.push({ name, value, sensitive: /authorization|cookie|token|api[-_]key/i.test(name), masked: /authorization|cookie|token|api[-_]key/i.test(name) })
  for (let i = 0; i < tokens.length; i++) {
    let option = tokens[i], inline: string | undefined
    if (option.startsWith('--') && option.includes('=')) {
      const split = option.indexOf('='); inline = option.slice(split + 1); option = option.slice(0, split)
    } else if (/^-[XHduAb].+/.test(option)) {
      inline = option.slice(2); option = option.slice(0, 2)
    }
    const value = () => {
      const result = inline ?? tokens[++i]
      if (result === undefined) throw new Error(`Missing value for ${option}.`)
      return result
    }
    if (['-L', '--location', '--compressed', '-s', '--silent', '-S', '--show-error', '-sS'].includes(option)) continue
    if (option === '-I' || option === '--head') { head = true; continue }
    if (option === '-G' || option === '--get') { get = true; continue }
    if (option === '-X' || option === '--request') { method = value().toUpperCase(); continue }
    if (option === '-H' || option === '--header') {
      const raw = value(), colon = raw.indexOf(':')
      if (colon < 1) throw new Error('Headers must use Name: value format.')
      header(raw.slice(0, colon).trim(), raw.slice(colon + 1).trim()); continue
    }
    if (['-d', '--data', '--data-raw', '--data-binary', '--data-urlencode', '--json'].includes(option)) {
      let body = value()
      if (option !== '--data-raw' && body.startsWith('@')) throw new Error('File bodies are not supported. Paste the body directly.')
      if (option === '--data-urlencode') {
        const split = body.indexOf('=')
        if (body.includes('@') && split < 0) throw new Error('File bodies are not supported.')
        body = split < 0 ? encodeURIComponent(body) : body.slice(0, split + 1) + encodeURIComponent(body.slice(split + 1))
      }
      json ||= option === '--json'
      data.push(body); continue
    }
    if (option === '-A' || option === '--user-agent') { header('User-Agent', value()); continue }
    if (option === '-b' || option === '--cookie') {
      const cookie = value()
      if (!cookie.includes('=')) throw new Error('Cookie files are not supported. Paste cookie values directly.')
      header('Cookie', cookie); continue
    }
    if (option === '-u' || option === '--user') {
      const credentials = value()
      if (!credentials.includes(':')) throw new Error('Basic authentication requires username:password.')
      header('Authorization', `Basic ${btoa(Array.from(new TextEncoder().encode(credentials), byte => String.fromCharCode(byte)).join(''))}`); continue
    }
    if (option === '--url') option = value()
    else if (option.startsWith('-')) throw new Error(`Unsupported curl option: ${option}. Remove it or configure the request manually.`)
    if (url) throw new Error('Import one URL at a time.')
    url = option
  }
  let parsed: URL
  try { parsed = new URL(url) } catch { throw new Error('Provide a valid HTTP or HTTPS URL.') }
  if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error('Only HTTP and HTTPS URLs are supported.')
  method ||= head ? 'HEAD' : get ? 'GET' : data.length ? 'POST' : 'GET'
  if (!['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].includes(method)) throw new Error(`Unsupported HTTP method: ${method}.`)
  const body = data.join(json ? '' : '&')
  if (get && data.length) {
    const hashIndex = url.indexOf('#')
    const base = hashIndex < 0 ? url : url.slice(0, hashIndex)
    const hash = hashIndex < 0 ? '' : url.slice(hashIndex)
    url = `${base}${base.includes('?') ? '&' : '?'}${body}${hash}`
  }
  if (data.length && !get && !headers.some(h => h.name.toLowerCase() === 'content-type')) header('Content-Type', json ? 'application/json' : 'application/x-www-form-urlencoded')
  if (json && !headers.some(h => h.name.toLowerCase() === 'accept')) header('Accept', 'application/json')
  return { url, http_method: method, headers, request_body_enabled: !get && body.length > 0, request_body: get ? '' : body }
}
