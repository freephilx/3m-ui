import client, { withNetworkRetry } from './client';

/** Website-audit blocklist entry (server-side REJECT enforcement). */
export interface BlockEntry {
  /** domain | domain-suffix | domain-keyword | geosite | ip-cidr; empty = auto-detect */
  type: string;
  value: string;
  remark?: string;
  enabled: boolean;
}

export const fetchBlocklist = () =>
  withNetworkRetry(() => client.get<BlockEntry[]>('/config/blocklist').then((r) => r.data));

export const saveBlocklist = (entries: BlockEntry[]) =>
  withNetworkRetry(() =>
    client.put<BlockEntry[]>('/config/blocklist', entries).then((r) => r.data),
  );

/** Block types exposed by the UI; labels are resolved via i18n. */
export const BLOCK_TYPES = [
  'domain',
  'domain-suffix',
  'domain-keyword',
  'geosite',
  'ip-cidr',
] as const;

/** Mirrors the backend rule rendering so the UI can preview enforcement. */
export const blockRuleLine = (e: BlockEntry): string => {
  switch (e.type) {
    case 'ip-cidr':
      return `IP-CIDR,${e.value},REJECT`;
    case 'domain':
      return `DOMAIN,${e.value},REJECT`;
    case 'domain-keyword':
      return `DOMAIN-KEYWORD,${e.value},REJECT`;
    case 'geosite':
      return `GEOSITE,${e.value},REJECT`;
    default:
      return `DOMAIN-SUFFIX,${e.value},REJECT`;
  }
};
