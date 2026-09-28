/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { ShareCenterManage } from '@/features/share-center/components'
import { getModuleAccessForGuard } from '@/lib/nav-modules'
import { useAuthStore } from '@/stores/auth-store'

// A sub-page of the share center rather than a console section. Writing needs an
// account, so this route always requires sign-in even when browsing is public.
export const Route = createFileRoute('/share-center/mine')({
  validateSearch: (search: Record<string, unknown>) => ({
    compose: search.compose === true || search.compose === 'true',
  }),
  beforeLoad: async ({ context, location }) => {
    const access = await getModuleAccessForGuard(
      context.queryClient,
      'shareCenter'
    )
    if (!access.enabled) throw redirect({ to: '/' })
    if (!useAuthStore.getState().auth.user) {
      throw redirect({ to: '/sign-in', search: { redirect: location.href } })
    }
  },
  component: ShareCenterMinePage,
})

function ShareCenterMinePage() {
  const { compose } = Route.useSearch()
  return <ShareCenterManage compose={compose} />
}
