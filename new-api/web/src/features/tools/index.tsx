import { Link } from '@tanstack/react-router'
import { Gauge, LockKeyhole, SearchCheck, TimerReset } from 'lucide-react'

import { PublicLayout } from '@/components/layout/components/public-layout'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const tools = [
  { icon: Gauge, title: 'API 测速', description: '为指定中转或 API 地址测量连接建立、首字节和完整响应时间。', state: '待接入安全测试接口' },
  { icon: TimerReset, title: '吞吐测试', description: '记录流式输出的首 Token 延迟和 Token 吞吐，结果按请求保存。', state: '规划中' },
  { icon: SearchCheck, title: '响应质量信号', description: '检查响应是否为空、是否重复、是否明显截断，提供可解释的信号。', state: '规划中' },
  { icon: LockKeyhole, title: '安全检查', description: '对用户主动提交的内容做基础的敏感信息和凭据泄露提示。', state: '规划中' },
]

export function ToolsPage() {
  return (
    <PublicLayout>
      <div className='space-y-8'>
        <div><p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>开放工具</p><h1 className='mt-1 text-3xl font-semibold tracking-tight'>工具</h1><p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-6'>把测速、连接检查和响应质量信号集中在一个入口。首版只展示能力边界，不伪造测试结果，也不会要求把上游 Key 交给第三方。</p></div>
        <div className='grid gap-4 md:grid-cols-2'>
          {tools.map(({ icon: Icon, title, description, state }) => <Card key={title} className='h-full'><CardHeader><div className='flex items-start justify-between gap-3'><div className='flex items-center gap-3'><div className='bg-primary/10 text-primary rounded-lg p-2'><Icon className='size-5' /></div><CardTitle className='text-lg'>{title}</CardTitle></div><Badge variant='outline'>{state}</Badge></div></CardHeader><CardContent><p className='text-muted-foreground text-sm leading-6'>{description}</p></CardContent></Card>)}
        </div>
        <Card><CardHeader><CardTitle>使用边界</CardTitle></CardHeader><CardContent className='space-y-2 text-sm leading-6'><p>测速需要由服务端发起受控请求，并明确测试地址、模型和凭据的保存期限。</p><p>质量信号只能作为排查线索，不能替代人工评测，也不能宣称可以准确识别“掺水”内容。</p><p>正式接入前会优先复用基座适配器、已有渠道测试和成熟的基准工具，减少重复实现。</p></CardContent></Card>
        <p className='text-muted-foreground text-xs'>想分享可用入口，请前往 <Link to='/relay-share' className='text-primary hover:underline'>中转分享</Link>。</p>
      </div>
    </PublicLayout>
  )
}
