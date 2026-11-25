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
import { useApiBaseUrl } from '@/features/adty-site/api'
import { useStatus } from '@/hooks/use-status'

import { ApiKeysDeleteDialog } from './api-keys-delete-dialog'
import { ApiKeysMutateDrawer } from './api-keys-mutate-drawer'
import { useApiKeys } from './api-keys-provider'
import { CCSwitchDialog } from './dialogs/cc-switch-dialog'

function normalizeServerAddress(raw: unknown): string {
  return typeof raw === 'string' ? raw.trim().replace(/\/+$/, '') : ''
}

export function ApiKeysDialogs() {
  const {
    open,
    setOpen,
    currentRow,
    initialCreateGroup,
    setInitialCreateGroup,
    resolvedKey,
  } = useApiKeys()
  const { status } = useStatus()
  const apiBaseUrl = useApiBaseUrl()
  const serverAddress =
    normalizeServerAddress(apiBaseUrl) ||
    normalizeServerAddress(status?.server_address) ||
    normalizeServerAddress(status?.data?.server_address)

  return (
    <>
      <ApiKeysMutateDrawer
        open={open === 'create' || open === 'update'}
        onOpenChange={(isOpen) => {
          if (!isOpen) {
            setOpen(null)
            setInitialCreateGroup(null)
          }
        }}
        currentRow={open === 'update' ? currentRow || undefined : undefined}
        initialGroup={
          open === 'create' ? initialCreateGroup || undefined : undefined
        }
      />
      <ApiKeysDeleteDialog />
      <CCSwitchDialog
        open={open === 'cc-switch'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        tokenKey={resolvedKey}
        endpoint={serverAddress}
      />
    </>
  )
}
