/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getResellerEdgeStatus,
  probeResellerEdge,
  retryResellerEdgeCert,
  setResellerEdgeFailover,
  siteAdminKeys,
  syncResellerEdgeDNS,
} from '../api'

import type { ResellerEdgeNodeProbe, ResellerEdgeProbe } from '../types'

function certStatusVariant(status: string | undefined): StatusVariant {
  switch (status) {
    case 'ready':
      return 'success'
    case 'failed':
      return 'danger'
    case 'issuing':
      return 'info'
    default:
      return 'warning'
  }
}

function certStatusLabel(t: TFunction, status: string | undefined): string {
  switch (status) {
    case 'ready':
      return t('Certificate ready')
    case 'issuing':
      return t('Issuing certificate')
    case 'failed':
      return t('Issuance failed')
    default:
      return t('Waiting for DNS')
  }
}

function probeVariant(probe: ResellerEdgeProbe | null | undefined): StatusVariant {
  if (!probe || !probe.checked_at) return 'neutral'
  const total = probe.total_count ?? (probe.nodes?.length || 1)
  const healthy = probe.healthy_count ?? (probe.healthy ? total : 0)
  if (healthy >= total && total > 0) return 'success'
  if (healthy > 0) return 'warning'
  return 'danger'
}

function probeLabel(t: TFunction, probe: ResellerEdgeProbe | null | undefined): string {
  if (!probe || !probe.checked_at) return t('No data')
  const total = probe.total_count ?? (probe.nodes?.length || 1)
  const healthy = probe.healthy_count ?? (probe.healthy ? total : 0)
  if (total > 1) {
    if (healthy >= total) {
      return t('Healthy ({{healthy}}/{{total}})', { healthy, total })
    }
    if (healthy > 0) {
      return t('Degraded ({{healthy}}/{{total}})', { healthy, total })
    }
    return t('Unhealthy ({{healthy}}/{{total}})', { healthy, total })
  }
  return probe.healthy ? t('Healthy') : t('Unhealthy')
}

function autoActionText(t: TFunction, action: string | undefined): string {
  switch (action) {
    case 'ejected_unhealthy':
      return t('Auto-ejected unhealthy node from DNS')
    case 'auto_failover_cf':
      return t('All nodes failed; auto-failed over to Cloudflare')
    case 'recovered_all':
      return t('Recovered healthy nodes back to DNS')
    default:
      return ''
  }
}

export function EdgeStatusCard(props: { canManage: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmFailover, setConfirmFailover] = useState<boolean | null>(null)

  const statusQuery = useQuery({
    queryKey: siteAdminKeys.edgeStatus,
    queryFn: getResellerEdgeStatus,
  })
  const status = statusQuery.data

  const refreshStatus = () => {
    void queryClient.invalidateQueries({ queryKey: siteAdminKeys.edgeStatus })
    void queryClient.invalidateQueries({ queryKey: siteAdminKeys.settings })
  }

  const syncDNS = useMutation({
    mutationFn: syncResellerEdgeDNS,
    onSuccess: () => {
      toast.success(t('Mainland DNS synced'))
      refreshStatus()
    },
    onError: (err) => handleServerError(err),
  })

  const toggleFailover = useMutation({
    mutationFn: (failoverToCF: boolean) => setResellerEdgeFailover(failoverToCF),
    onSuccess: (res) => {
      setConfirmFailover(null)
      toast.success(
        res.failover_to_cf
          ? t('Switched mainland traffic to Cloudflare')
          : t('Switched mainland traffic back to edge node')
      )
      refreshStatus()
    },
    onError: (err) => handleServerError(err),
  })

  const runProbe = useMutation({
    mutationFn: probeResellerEdge,
    onSuccess: () => {
      toast.success(t('Health probe completed'))
      refreshStatus()
    },
    onError: (err) => handleServerError(err),
  })

  const retryCert = useMutation({
    mutationFn: (certName: string) => retryResellerEdgeCert(certName),
    onSuccess: () => {
      toast.success(t('Certificate check queued'))
      refreshStatus()
    },
    onError: (err) => handleServerError(err),
  })

  if (!status) {
    return null
  }

  const wc = status.wildcard_cert
  const mc = status.main_site_cert
  const pull = status.manifest_pull
  const dns = status.dns_sync
  const probe = status.probe
  let configuredIPs: string[] = []
  if (status.edge_node_ips && status.edge_node_ips.length > 0) {
    configuredIPs = status.edge_node_ips
  } else if (status.edge_node_ip) {
    configuredIPs = [status.edge_node_ip]
  }
  let syncedIPs: string[] = []
  if (dns?.ips && dns.ips.length > 0) {
    syncedIPs = dns.ips
  } else if (dns?.ip) {
    syncedIPs = [dns.ip]
  }
  const syncedIPSet = new Set(syncedIPs)
  const probeNodesByIP = new Map<string, ResellerEdgeNodeProbe>()
  for (const node of probe?.nodes ?? []) {
    probeNodesByIP.set(node.ip, node)
  }
  const canRunDNSActions =
    props.canManage &&
    configuredIPs.length > 0 &&
    Boolean(status.domain_mainland_target)

  let dnsTargetSuffix = ''
  if (dns && dns.synced_at > 0) {
    if (dns.proxied) {
      dnsTargetSuffix = ' -> Cloudflare'
    } else if (syncedIPs.length > 0) {
      dnsTargetSuffix = ` -> ${syncedIPs.join(', ')}`
    }
  } else if (configuredIPs.length > 0) {
    dnsTargetSuffix = ` -> ${configuredIPs.join(', ')}`
  }

  let dnsModeVariant: StatusVariant = 'neutral'
  let dnsModeLabel = t('Not synced yet')
  if (dns && dns.synced_at > 0) {
    if (dns.proxied) {
      dnsModeVariant = 'warning'
      dnsModeLabel =
        probe?.auto_failover_active || dns.auto_mode
          ? t('Auto failover to Cloudflare')
          : t('Cloudflare failover (proxied)')
    } else {
      dnsModeVariant =
        configuredIPs.length > 1 && syncedIPs.length < configuredIPs.length
          ? 'warning'
          : 'success'
      dnsModeLabel =
        syncedIPs.length > 1
          ? t('Direct HK edge ({{count}} nodes)', { count: syncedIPs.length })
          : t('Direct HK edge (DNS only)')
    }
  }

  const autoActionDesc = autoActionText(t, probe?.last_auto_action)

  return (
    <div className='border-border space-y-3 border-t pt-4 sm:col-span-2'>
      <div className='grid gap-3 md:grid-cols-2'>
        <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3.5 text-xs'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='font-medium'>{t('Wildcard certificate')}</span>
            {wc && (
              <div className='flex items-center gap-1.5'>
                <Badge variant='outline'>
                  {wc.environment === 'staging'
                    ? t('Staging')
                    : t('Production')}
                </Badge>
                <StatusBadge
                  variant={certStatusVariant(wc.status)}
                  label={certStatusLabel(t, wc.status)}
                />
              </div>
            )}
          </div>
          {wc ? (
            <>
              <p className='font-mono'>{wc.cert_name}</p>
              {wc.not_after > 0 && (
                <p className='text-muted-foreground'>
                  {t('Expires {{date}}', {
                    date: formatTimestampToDate(wc.not_after),
                  })}
                </p>
              )}
              {wc.status !== 'ready' && wc.alias_fqdn && (
                <div className='bg-background space-y-1 rounded border p-2'>
                  <p className='text-muted-foreground'>
                    {t('Add CNAME on default line:')}
                  </p>
                  <div className='flex items-center justify-between gap-1.5'>
                    <span className='truncate font-mono'>
                      {wc.challenge_host}
                    </span>
                    <CopyButton value={wc.challenge_host} />
                  </div>
                  <div className='flex items-center justify-between gap-1.5'>
                    <span className='truncate font-mono'>{wc.alias_fqdn}</span>
                    <CopyButton value={wc.alias_fqdn} />
                  </div>
                </div>
              )}
              {wc.last_error && (
                <p className='text-destructive break-all'>{wc.last_error}</p>
              )}
            </>
          ) : (
            <p className='text-muted-foreground'>{t('Not configured')}</p>
          )}
          <p className='text-muted-foreground'>
            {t('Custom site certificates: {{ready}} / {{total}} ready', {
              ready: status.custom_cert_counts.ready,
              total: status.custom_cert_counts.total,
            })}
          </p>
          {props.canManage && wc && (
            <div className='pt-1'>
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={retryCert.isPending}
                onClick={() => retryCert.mutate('')}
              >
                {t('Retry certificate')}
              </Button>
            </div>
          )}
        </div>

        {mc && (
          <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3.5 text-xs'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <span className='font-medium'>{t('Main site certificate')}</span>
              <div className='flex items-center gap-1.5'>
                <Badge variant='outline'>
                  {mc.environment === 'staging'
                    ? t('Staging')
                    : t('Production')}
                </Badge>
                <StatusBadge
                  variant={certStatusVariant(mc.status)}
                  label={certStatusLabel(t, mc.status)}
                />
              </div>
            </div>
            <p className='font-mono'>{mc.cert_name}</p>
            {mc.not_after > 0 && (
              <p className='text-muted-foreground'>
                {t('Expires {{date}}', {
                  date: formatTimestampToDate(mc.not_after),
                })}
              </p>
            )}
            {mc.status !== 'ready' && mc.alias_fqdn && (
              <div className='bg-background space-y-1 rounded border p-2'>
                <p className='text-muted-foreground'>
                  {t('Add CNAME on default line:')}
                </p>
                <div className='flex items-center justify-between gap-1.5'>
                  <span className='truncate font-mono'>{mc.challenge_host}</span>
                  <CopyButton value={mc.challenge_host} />
                </div>
                <div className='flex items-center justify-between gap-1.5'>
                  <span className='truncate font-mono'>{mc.alias_fqdn}</span>
                  <CopyButton value={mc.alias_fqdn} />
                </div>
              </div>
            )}
            {mc.status === 'ready' && status.domain_mainland_target && (
              <div className='bg-background space-y-1 rounded border p-2'>
                <p className='text-muted-foreground'>
                  {t('Point Mainland China DNS line to:')}
                </p>
                <div className='flex items-center justify-between gap-1.5'>
                  <span className='truncate font-mono'>
                    {status.domain_mainland_target}
                  </span>
                  <CopyButton value={status.domain_mainland_target} />
                </div>
              </div>
            )}
            {mc.last_error && (
              <p className='text-destructive break-all'>{mc.last_error}</p>
            )}
            {props.canManage && (
              <div className='pt-1'>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={retryCert.isPending}
                  onClick={() => retryCert.mutate(mc.cert_name)}
                >
                  {t('Retry certificate')}
                </Button>
              </div>
            )}
          </div>
        )}

        <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3.5 text-xs'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='font-medium'>{t('Edge node sync')}</span>
            <StatusBadge
              variant={status.edge_token_configured ? 'success' : 'warning'}
              label={
                status.edge_token_configured
                  ? t('Configured')
                  : t('Not configured')
              }
            />
          </div>
          <p className='text-muted-foreground'>
            {t('Edge token')}:{' '}
            <span className='font-mono'>ADTY_EDGE_TOKEN</span>
          </p>
          <p className='text-muted-foreground'>
            {t('Last manifest pull')}:{' '}
            {pull && pull.pulled_at > 0
              ? `${formatTimestampToDate(pull.pulled_at)}${pull.client_ip ? ` (${pull.client_ip})` : ''}`
              : t('No data')}
          </p>
        </div>

        <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3.5 text-xs'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='font-medium'>{t('Mainland DNS & Failover')}</span>
            <StatusBadge variant={dnsModeVariant} label={dnsModeLabel} />
          </div>
          {status.domain_mainland_target && (
            <p className='font-mono break-all'>
              {status.domain_mainland_target}
              {dnsTargetSuffix}
            </p>
          )}
          <p className='text-muted-foreground'>
            {t('Last synced')}:{' '}
            {dns && dns.synced_at > 0
              ? `${formatTimestampToDate(dns.synced_at)} · TTL ${dns.ttl}`
              : t('No data')}
          </p>
          {autoActionDesc && probe?.last_auto_action_at ? (
            <p className='text-muted-foreground'>
              {autoActionDesc} · {formatTimestampToDate(probe.last_auto_action_at)}
            </p>
          ) : null}
          {dns?.last_error && (
            <p className='text-destructive break-all'>{dns.last_error}</p>
          )}
          {props.canManage && (
            <div className='flex flex-wrap gap-2 pt-1'>
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={!canRunDNSActions || syncDNS.isPending}
                onClick={() => syncDNS.mutate()}
              >
                {t('Sync DNS now')}
              </Button>
              <Button
                type='button'
                size='sm'
                variant={status.edge_failover_to_cf ? 'brand' : 'destructive'}
                disabled={!canRunDNSActions || toggleFailover.isPending}
                onClick={() => setConfirmFailover(!status.edge_failover_to_cf)}
              >
                {status.edge_failover_to_cf
                  ? t('Switch back to edge node')
                  : t('Fail over to Cloudflare')}
              </Button>
            </div>
          )}
        </div>

        <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3.5 text-xs'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='font-medium'>{t('Origin health probe')}</span>
            <StatusBadge
              variant={probeVariant(probe)}
              label={probeLabel(t, probe)}
            />
          </div>
          {probe && probe.checked_at > 0 ? (
            <>
              <p className='text-muted-foreground'>
                {formatTimestampToDate(probe.checked_at)} ·{' '}
                <span className='font-mono'>{probe.probe_host}</span>
              </p>
              <p className='text-muted-foreground'>
                {t('HTTP status')}: {probe.http_status || '-'} · {t('Latency')}:{' '}
                {t('{{value}}ms', { value: probe.latency_ms })}
              </p>
              {probe.consecutive_failures > 0 && (
                <p className='text-destructive'>
                  {t('Consecutive failures: {{count}}', {
                    count: probe.consecutive_failures,
                  })}
                </p>
              )}
              {probe.last_error && (
                <p className='text-destructive break-all'>{probe.last_error}</p>
              )}
            </>
          ) : (
            <p className='text-muted-foreground'>{t('No data')}</p>
          )}
          {props.canManage && (
            <div className='pt-1'>
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={configuredIPs.length === 0 || runProbe.isPending}
                onClick={() => runProbe.mutate()}
              >
                {t('Probe now')}
              </Button>
            </div>
          )}
        </div>
      </div>

      {configuredIPs.length > 0 && (
        <div className='bg-muted/30 rounded-lg border p-3.5 text-xs'>
          <div className='mb-2 flex flex-wrap items-center justify-between gap-2'>
            <span className='font-medium'>{t('Edge nodes')}</span>
            <span className='text-muted-foreground'>
              {configuredIPs.length}
            </span>
          </div>
          <div className='overflow-x-auto'>
            <table className='w-full text-left'>
              <thead>
                <tr className='border-border text-muted-foreground border-b'>
                  <th className='py-1.5 pr-3 font-medium'>{t('Node IP')}</th>
                  <th className='py-1.5 pr-3 font-medium'>{t('DNS routing')}</th>
                  <th className='py-1.5 pr-3 font-medium'>
                    {t('Origin health probe')}
                  </th>
                  <th className='py-1.5 font-medium'>{t('Last pull')}</th>
                </tr>
              </thead>
              <tbody className='divide-border divide-y'>
                {configuredIPs.map((ip) => {
                  const nodeProbe = probeNodesByIP.get(ip)
                  const nodePull = pull?.nodes?.[ip]
                  let dnsBadgeVariant: StatusVariant = 'neutral'
                  let dnsBadgeLabel = t('Pending sync')
                  if (
                    status.edge_failover_to_cf ||
                    probe?.auto_failover_active ||
                    (dns && dns.synced_at > 0 && dns.proxied)
                  ) {
                    dnsBadgeVariant = 'warning'
                    dnsBadgeLabel = t('Cloudflare takeover')
                  } else if (nodeProbe?.ejected) {
                    dnsBadgeVariant = 'danger'
                    dnsBadgeLabel = t('Auto-ejected')
                  } else if (dns && dns.synced_at > 0 && syncedIPSet.has(ip)) {
                    dnsBadgeVariant = 'success'
                    dnsBadgeLabel = t('In DNS')
                  }
                  return (
                    <tr key={ip} className='align-top'>
                      <td className='py-2 pr-3 font-mono whitespace-nowrap'>
                        {ip}
                      </td>
                      <td className='py-2 pr-3 whitespace-nowrap'>
                        <StatusBadge
                          variant={dnsBadgeVariant}
                          label={dnsBadgeLabel}
                        />
                      </td>
                      <td className='py-2 pr-3'>
                        {nodeProbe && nodeProbe.checked_at > 0 ? (
                          <div className='space-y-0.5'>
                            <div className='flex flex-wrap items-center gap-1.5'>
                              <StatusBadge
                                variant={
                                  nodeProbe.healthy ? 'success' : 'danger'
                                }
                                label={
                                  nodeProbe.healthy
                                    ? t('Healthy')
                                    : t('Unhealthy')
                                }
                              />
                              <span className='text-muted-foreground'>
                                {nodeProbe.http_status || '-'} ·{' '}
                                {t('{{value}}ms', {
                                  value: nodeProbe.latency_ms,
                                })}
                              </span>
                              {nodeProbe.consecutive_failures > 0 && (
                                <span className='text-destructive'>
                                  {t('Failures: {{count}}', {
                                    count: nodeProbe.consecutive_failures,
                                  })}
                                </span>
                              )}
                            </div>
                            {nodeProbe.last_error && (
                              <p className='text-destructive break-all'>
                                {nodeProbe.last_error}
                              </p>
                            )}
                          </div>
                        ) : (
                          <span className='text-muted-foreground'>
                            {t('No data')}
                          </span>
                        )}
                      </td>
                      <td className='text-muted-foreground py-2 whitespace-nowrap'>
                        {nodePull && nodePull.pulled_at > 0
                          ? formatTimestampToDate(nodePull.pulled_at)
                          : t('No data')}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={confirmFailover !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmFailover(null)
        }}
        destructive={confirmFailover === true}
        title={
          confirmFailover
            ? t('Fail over mainland traffic to Cloudflare?')
            : t('Switch mainland traffic back to the edge node?')
        }
        desc={
          confirmFailover
            ? t(
                'This updates the mainland CNAME target in Cloudflare DNS to orange-cloud proxy mode so mainland homepage traffic bypasses all Hong Kong edge nodes.'
              )
            : t(
                'This updates the mainland CNAME target in Cloudflare DNS to DNS-only mode (TTL 60s) pointing directly to all configured Hong Kong edge node IPs.'
              )
        }
        confirmText={
          confirmFailover
            ? t('Fail over to Cloudflare')
            : t('Switch back to edge node')
        }
        isLoading={toggleFailover.isPending}
        handleConfirm={() => {
          if (confirmFailover !== null) {
            toggleFailover.mutate(confirmFailover)
          }
        }}
      />
    </div>
  )
}
