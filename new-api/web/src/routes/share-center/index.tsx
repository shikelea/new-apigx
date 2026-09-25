/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { getModuleAccessForGuard } from '@/lib/nav-modules'
import { useAuthStore } from '@/stores/auth-store'

import { ShareCenterBrowser } from '@/features/share-center/components'

export const Route = createFileRoute('/share-center/')({
  beforeLoad: async ({ context, location }) => {
    const access = await getModuleAccessForGuard(
      context.queryClient,
      'shareCenter'
    )
    if (!access.enabled) throw redirect({ to: '/' })
    if (access.requireAuth && !useAuthStore.getState().auth.user) {
      throw redirect({ to: '/sign-in', search: { redirect: location.href } })
    }
  },
  component: ShareCenterBrowser,
})
