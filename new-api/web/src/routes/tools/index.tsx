import { createFileRoute } from '@tanstack/react-router'

import { ToolsPage } from '@/features/tools'

export const Route = createFileRoute('/tools/')({ component: ToolsPage })
