import type { NodeData } from '@/stores/nodes'
import type { IpGeo } from '@/utils/ipGeoHelper'
import type { ProviderResolveResult } from '@/utils/providerInfo'
import { lookupIpGeo } from '@/utils/ipGeoHelper'
import { resolveProviderInfo } from '@/utils/providerInfo'

export interface NodeProviderMetadata {
  provider: ProviderResolveResult | null
  geo: IpGeo | null
  loading: boolean
}

const FINGERPRINT_SEPARATOR = ''

export function getNodeIps(node: NodeData): string[] {
  return [node.ipv4, node.ipv6].filter((ip): ip is string => Boolean(ip?.trim()))
}

export function getProviderMetadataText(node: NodeData): string {
  return [node.name, node.public_remark, node.remark, node.tags, node.group, node.region]
    .filter(Boolean)
    .join(' ')
}

export function getNodeProviderFingerprint(node: NodeData, customAliases: string, allowGeoLookup: boolean): string {
  return [
    node.uuid,
    node.name,
    node.public_remark,
    node.remark,
    node.tags,
    node.group,
    node.region,
    allowGeoLookup ? node.ipv4 : '',
    allowGeoLookup ? node.ipv6 : '',
    customAliases,
    allowGeoLookup ? 'geo' : 'metadata-only',
  ].join(FINGERPRINT_SEPARATOR)
}

export async function lookupNodeGeo(node: NodeData): Promise<IpGeo | null> {
  for (const ip of getNodeIps(node)) {
    const geo = await lookupIpGeo(ip)
    if (geo)
      return geo
  }
  return null
}

export function buildNodeProviderMetadata(node: NodeData, customAliases: string, geo: IpGeo | null, loading: boolean): NodeProviderMetadata {
  return {
    geo,
    loading,
    provider: resolveProviderInfo({
      metadata: getProviderMetadataText(node),
      org: geo?.org,
      asn: geo?.asn,
      customAliases,
    }),
  }
}
