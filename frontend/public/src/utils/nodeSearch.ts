import type { NodeData } from '@/stores/nodes'
import { isRegionMatch } from '@/utils/regionHelper'

const SEARCH_TERM_SEPARATOR = /\s+/

function normalizeSearchValue(value: unknown): string {
  return String(value ?? '').normalize('NFKC').toLocaleLowerCase().trim()
}

function matchesIpv4Pattern(ip: string | undefined, pattern: string): boolean {
  if (!ip)
    return false

  const patternParts = pattern.split('.')
  const ipParts = ip.split('.')
  if (patternParts.length !== 4 || ipParts.length !== 4)
    return false

  return patternParts.every((part, index) => part === 'x' || part === '*' || part === ipParts[index])
}

function matchesNodeIp(node: NodeData, term: string): boolean {
  const ipv4 = normalizeSearchValue(node.ipv4)
  const ipv6 = normalizeSearchValue(node.ipv6)
  return ipv4.includes(term)
    || ipv6.includes(term)
    || matchesIpv4Pattern(ipv4, term)
}

export function isNodeMatchSearch(node: NodeData, search: string): boolean {
  const normalizedSearch = normalizeSearchValue(search)
  if (!normalizedSearch)
    return true

  const searchableText = [
    node.name,
    node.uuid,
    node.cpu_name,
    node.gpu_name,
    node.os,
    node.kernel_version,
    node.virtualization,
    node.arch,
    node.region,
    node.group,
    ...node.groups,
    node.tags,
    node.public_remark,
    node.remark,
  ].map(normalizeSearchValue).filter(Boolean).join('\n')

  return normalizedSearch.split(SEARCH_TERM_SEPARATOR).every(term =>
    searchableText.includes(term)
    || matchesNodeIp(node, term)
    || Boolean(node.region && isRegionMatch(node.region, term)),
  )
}
