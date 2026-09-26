import { Link } from '@tanstack/react-router'
import { ArrowRight, RadioTower } from 'lucide-react'

import { PublicLayout } from '@/components/layout/components/public-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const relayTypes = [
  { title: '公益中转站', description: '分享公开可用的中转入口、支持模型和使用限制。', status: '规划中' },
  { title: '个人中转站', description: '分享自建或团队维护的中转服务，保留联系方式和更新时间。', status: '规划中' },
  { title: '临时福利入口', description: '记录限时活动、试用额度与失效时间，避免把临时信息混进模型广场。', status: '规划中' },
]

export function RelayShare() {
  return (
    <PublicLayout>
      <div className='space-y-8'>
        <div className='flex flex-wrap items-end justify-between gap-4'>
          <div>
            <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>社区资料</p>
            <h1 className='mt-1 text-3xl font-semibold tracking-tight'>中转分享</h1>
            <p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-6'>分享中转站和代理站的文字资料。这里按内容和最近更新时间浏览，不做排名，也不把中转站包装成官方服务。</p>
          </div>
          <Button render={<Link to='/share-center/manage' />} variant='outline'><ArrowRight />提交分享</Button>
        </div>
        <section className='from-primary/10 via-background to-background rounded-2xl border bg-gradient-to-br p-6 md:p-8'>
          <div className='flex items-start gap-4'>
            <div className='bg-primary/10 text-primary rounded-xl p-3'><RadioTower /></div>
            <div><h2 className='text-xl font-semibold'>先做清晰的资料页，再接入可用性投票</h2><p className='text-muted-foreground mt-2 text-sm leading-6'>首版只承载 Markdown 文本、服务说明、适用范围和最后验证时间。后续可以复用分享中心的审核、投票、权限和作者体系。</p></div>
          </div>
        </section>
        <div className='grid gap-4 md:grid-cols-3'>
          {relayTypes.map((item) => <Card key={item.title} className='h-full'><CardHeader><div className='flex items-start justify-between gap-3'><CardTitle className='text-lg'>{item.title}</CardTitle><Badge variant='outline'>{item.status}</Badge></div></CardHeader><CardContent><p className='text-muted-foreground text-sm leading-6'>{item.description}</p></CardContent></Card>)}
        </div>
        <Card><CardHeader><CardTitle>发布规则</CardTitle></CardHeader><CardContent className='grid gap-3 text-sm md:grid-cols-3'><Rule title='纯文本 / Markdown' /><Rule title='显示作者与更新时间' /><Rule title='不展示综合排名' /></CardContent></Card>
        <p className='text-muted-foreground text-xs'>如需查看已经发布的文字分享，请前往 <Link to='/share-center' className='text-primary hover:underline'>分享中心</Link>。</p>
      </div>
    </PublicLayout>
  )
}

function Rule({ title }: { title: string }) { return <div className='bg-muted/30 rounded-lg border p-3'>{title}</div> }
