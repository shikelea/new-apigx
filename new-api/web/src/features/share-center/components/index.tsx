/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  CalendarClock,
  CheckCircle2,
  CircleCheck,
  Clock3,
  FileText,
  Search,
  ShieldCheck,
  ThumbsDown,
  ThumbsUp,
  UserRound,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout/components/public-layout'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { LoadingState } from '@/components/loading-state'
import { PageTransition } from '@/components/page-transition'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Markdown } from '@/components/ui/markdown'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  archiveShareCenterPost,
  createShareCenterPost,
  getMyShareCenterPosts,
  getShareCenterConfig,
  getShareCenterPost,
  getShareCenterPosts,
  getShareCenterReviews,
  reviewShareCenterPost,
  submitShareCenterPost,
  updateShareCenterConfig,
  updateShareCenterPost,
  voteShareCenterPost,
} from '../api'
import {
  formatPublishedAt,
  postState,
  voteSummary,
  type ShareFilter,
} from '../lib/format'
import type { SharePost } from '../types'

function postStatusLabel(status: SharePost['status']) {
  return {
    draft: '草稿',
    pending: '待审核',
    published: '已发布',
    rejected: '需修改',
    archived: '已归档',
  }[status]
}

const SHARE_FILTERS: { id: ShareFilter; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'usable', label: '仍可用' },
  { id: 'observing', label: '待观察' },
  { id: 'invalid', label: '可能失效' },
]

export function SharePostCard({ post }: { post: SharePost }) {
  const state = postState(post)
  const badgeVariant = state === 'usable' ? 'secondary' : 'outline'
  return (
    <Link
      to='/share-center/$publicID'
      params={{ publicID: post.public_id }}
      className='group block h-full'
    >
      <Card className='border-border/80 bg-card/90 group-hover:border-primary/40 group-focus-visible:ring-ring h-full transition-[transform,box-shadow,border-color] duration-200 group-hover:-translate-y-0.5 group-hover:shadow-lg group-focus-visible:ring-2'>
        <CardHeader className='gap-4 pb-1'>
          <div className='flex items-start justify-between gap-3'>
            <div className='flex min-w-0 items-start gap-3'>
              <div className='bg-primary/10 text-primary mt-0.5 rounded-lg p-2'>
                <FileText className='size-4' />
              </div>
              <div className='min-w-0'>
                <CardTitle className='line-clamp-2 text-base leading-6'>
                  {post.title}
                </CardTitle>
                <div className='text-muted-foreground mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs'>
                  <span className='inline-flex items-center gap-1'>
                    <UserRound className='size-3.5' />
                    {post.author_name || '分享者'}
                  </span>
                  <span aria-hidden>·</span>
                  <span className='inline-flex items-center gap-1'>
                    <CalendarClock className='size-3.5' />
                    {formatPublishedAt(post.published_at || post.updated_at)}
                  </span>
                </div>
              </div>
            </div>
            <Badge
              variant={badgeVariant}
              className='shrink-0 whitespace-nowrap'
            >
              {voteSummary(post)}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className='flex flex-1 flex-col justify-between gap-4'>
          <p className='text-muted-foreground line-clamp-3 text-sm leading-6'>
            {post.summary ||
              post.content.replaceAll(/[#*_`>]/g, '').slice(0, 180)}
          </p>
          <div className='text-muted-foreground flex items-center justify-between border-t pt-3 text-xs'>
            <span>有效性反馈</span>
            <span className='text-foreground font-medium tabular-nums'>
              {post.usable_votes} 可用 · {post.invalid_votes} 失效
            </span>
          </div>
        </CardContent>
      </Card>
    </Link>
  )
}

export function ShareCenterBrowser() {
  const [keyword, setKeyword] = useState('')
  const [submittedKeyword, setSubmittedKeyword] = useState('')
  const [filter, setFilter] = useState<ShareFilter>('all')
  const query = useQuery({
    queryKey: ['share-center-posts', submittedKeyword],
    queryFn: () => getShareCenterPosts(submittedKeyword),
  })

  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />

  const posts = query.data?.data?.items ?? []
  const visiblePosts = posts.filter(
    (post) => filter === 'all' || postState(post) === filter
  )
  const usableCount = posts.filter(
    (post) => postState(post) === 'usable'
  ).length
  const voteCount = posts.reduce(
    (total, post) => total + post.usable_votes + post.invalid_votes,
    0
  )

  return (
    <PublicLayout showMainContainer={false}>
      <div className='relative'>
        <div
          aria-hidden
          className='pointer-events-none absolute inset-x-0 top-0 h-[620px] opacity-20 dark:opacity-[0.1]'
          style={{
            background: [
              'radial-gradient(ellipse 60% 50% at 16% 14%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
              'radial-gradient(ellipse 52% 42% at 84% 10%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
              'radial-gradient(ellipse 42% 38% at 52% 68%, oklch(0.7 0.12 280 / 40%) 0%, transparent 70%)',
            ].join(', '),
            maskImage:
              'linear-gradient(to bottom, black 42%, transparent 100%)',
            WebkitMaskImage:
              'linear-gradient(to bottom, black 42%, transparent 100%)',
          }}
        />
        <PageTransition className='relative mx-auto w-full max-w-[1280px] space-y-8 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <section className='flex flex-wrap items-end justify-between gap-5'>
            <div className='max-w-3xl'>
              <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
                社区资料库
              </p>
              <h1 className='mt-2 text-[clamp(2rem,4vw,3rem)] leading-[1.1] font-bold tracking-tight'>
                分享中心
              </h1>
              <p className='text-muted-foreground mt-3 max-w-2xl text-sm leading-6 sm:text-base'>
                分享真实可用的信息，让下一位少走弯路。这里收录白嫖渠道、福利活动、教程和使用经验；可用性由社区反馈和管理员状态共同呈现。
              </p>
            </div>
            <div className='flex shrink-0 flex-wrap gap-2'>
              <Button variant='outline' render={<Link to='/relay-share' />}>
                中转分享
              </Button>
              <Button render={<Link to='/share-center/manage' />}>
                <FileText />
                我的分享
              </Button>
            </div>
          </section>

          <section className='grid gap-3 sm:grid-cols-3'>
            {[
              { label: '已发布分享', value: posts.length, icon: FileText },
              { label: '当前多数可用', value: usableCount, icon: CircleCheck },
              { label: '有效性反馈', value: voteCount, icon: ShieldCheck },
            ].map(({ label, value, icon: Icon }) => (
              <Card
                key={label}
                size='sm'
                className='bg-card/75 backdrop-blur-sm'
              >
                <CardContent className='flex items-center gap-3 p-4'>
                  <div className='bg-primary/10 text-primary rounded-lg p-2'>
                    <Icon className='size-4' />
                  </div>
                  <div>
                    <div className='text-foreground text-xl font-semibold tabular-nums'>
                      {value}
                    </div>
                    <div className='text-muted-foreground text-xs'>{label}</div>
                  </div>
                </CardContent>
              </Card>
            ))}
          </section>

          <Card className='bg-card/85 backdrop-blur-sm'>
            <CardContent className='space-y-4 p-4 sm:p-5'>
              <div className='flex flex-col gap-3 sm:flex-row'>
                <div className='relative flex-1'>
                  <Search className='text-muted-foreground absolute top-2.5 left-3 size-4' />
                  <Input
                    value={keyword}
                    onChange={(event) => setKeyword(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') setSubmittedKeyword(keyword)
                    }}
                    placeholder='搜索标题或摘要'
                    className='h-10 pl-9'
                  />
                </div>
                <Button onClick={() => setSubmittedKeyword(keyword)}>
                  搜索分享
                </Button>
              </div>
              <div
                className='flex flex-wrap items-center gap-2'
                role='tablist'
                aria-label='分享状态'
              >
                {SHARE_FILTERS.map((item) => (
                  <button
                    key={item.id}
                    type='button'
                    role='tab'
                    aria-selected={filter === item.id}
                    onClick={() => setFilter(item.id)}
                    className={cn(
                      'rounded-full border px-3 py-1.5 text-xs font-medium transition-colors',
                      filter === item.id
                        ? 'border-primary bg-primary text-primary-foreground'
                        : 'border-border/70 text-muted-foreground hover:border-primary/40 hover:text-foreground'
                    )}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
            </CardContent>
          </Card>

          <section className='space-y-4'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <h2 className='text-lg font-semibold tracking-tight'>
                  最新分享
                </h2>
                <p className='text-muted-foreground mt-1 text-sm'>
                  按更新时间浏览，不做综合排名。
                </p>
              </div>
              <span className='text-muted-foreground text-xs tabular-nums'>
                {visiblePosts.length} 条结果
              </span>
            </div>
            {visiblePosts.length === 0 ? (
              <div className='bg-card/70 rounded-xl border border-dashed px-6 py-12'>
                <EmptyState title='没有符合条件的分享' />
              </div>
            ) : (
              <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
                {visiblePosts.map((post) => (
                  <SharePostCard key={post.public_id} post={post} />
                ))}
              </div>
            )}
          </section>
        </PageTransition>
      </div>
    </PublicLayout>
  )
}

export function ShareCenterPostDetail({ publicID }: { publicID: string }) {
  const auth = useAuthStore((state) => state.auth)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['share-center-post', publicID],
    queryFn: () => getShareCenterPost(publicID),
  })
  const vote = useMutation({
    mutationFn: (value: 1 | -1) => voteShareCenterPost(publicID, value),
    onSuccess: () => {
      toast.success('感谢你的反馈')
      void queryClient.invalidateQueries({
        queryKey: ['share-center-post', publicID],
      })
    },
    onError: (error) => handleServerError(error),
  })

  if (query.isPending) {
    return <LoadingState />
  }
  if (query.isError || !query.data?.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  const post = query.data.data
  const state = postState(post)
  return (
    <PublicLayout showMainContainer={false}>
      <div className='relative'>
        <PageTransition className='mx-auto w-full max-w-[1180px] space-y-6 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div className='flex flex-wrap items-end justify-between gap-4'>
            <div className='min-w-0'>
              <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
                社区分享 · 详情
              </p>
              <h1 className='mt-2 text-[clamp(1.75rem,4vw,2.75rem)] leading-[1.12] font-bold tracking-tight'>
                {post.title}
              </h1>
              <div className='text-muted-foreground mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm'>
                <span className='inline-flex items-center gap-1.5'>
                  <UserRound className='size-4' />
                  {post.author_name || '分享者'}
                </span>
                <span aria-hidden>·</span>
                <span className='inline-flex items-center gap-1.5'>
                  <CalendarClock className='size-4' />
                  更新于{' '}
                  {formatPublishedAt(post.updated_at || post.published_at)}
                </span>
              </div>
            </div>
            <Button variant='outline' render={<Link to='/share-center' />}>
              返回分享中心
            </Button>
          </div>

          <div className='grid gap-6 lg:grid-cols-[minmax(0,1fr)_280px]'>
            <Card className='bg-card/90'>
              <CardContent className='p-6 sm:p-8'>
                <Markdown className='prose dark:prose-invert max-w-none text-sm leading-7 sm:text-base'>
                  {post.content}
                </Markdown>
              </CardContent>
            </Card>
            <aside className='space-y-4 lg:sticky lg:top-24 lg:self-start'>
              <Card className='bg-card/90'>
                <CardHeader>
                  <CardTitle className='text-base'>分享信息</CardTitle>
                </CardHeader>
                <CardContent className='space-y-4 text-sm'>
                  <div>
                    <span className='text-muted-foreground'>当前判断</span>
                    <p className='mt-1 font-medium'>{voteSummary(post)}</p>
                  </div>
                  <div>
                    <span className='text-muted-foreground'>发布时间</span>
                    <p className='mt-1 font-medium'>
                      {formatPublishedAt(post.published_at)}
                    </p>
                  </div>
                  <div className='bg-muted/40 rounded-lg p-3 text-xs leading-5'>
                    <Clock3 className='text-primary mb-1 size-4' />
                    状态会随着社区反馈变化，投票只作为排查线索。
                  </div>
                </CardContent>
              </Card>
              <Card className='bg-card/90'>
                <CardHeader>
                  <CardTitle className='text-base'>还能用吗？</CardTitle>
                </CardHeader>
                <CardContent className='space-y-3'>
                  <Button
                    className='w-full'
                    disabled={!auth?.user || vote.isPending}
                    onClick={() => vote.mutate(1)}
                  >
                    <ThumbsUp />
                    仍可用
                  </Button>
                  <Button
                    variant='outline'
                    className='w-full'
                    disabled={!auth?.user || vote.isPending}
                    onClick={() => vote.mutate(-1)}
                  >
                    <ThumbsDown />
                    已失效
                  </Button>
                  {!auth?.user ? (
                    <p className='text-muted-foreground text-xs'>
                      登录后可以参与投票。
                    </p>
                  ) : null}
                  {state === 'usable' ? (
                    <p className='text-muted-foreground text-xs'>
                      目前可用反馈较多。
                    </p>
                  ) : null}
                </CardContent>
              </Card>
            </aside>
          </div>
        </PageTransition>
      </div>
    </PublicLayout>
  )
}

type EditorProps = {
  post?: SharePost
  onSaved: () => void
  onCancel: () => void
}

export function SharePostEditor({ post, onSaved, onCancel }: EditorProps) {
  const [title, setTitle] = useState(post?.title ?? '')
  const [summary, setSummary] = useState(post?.summary ?? '')
  const [content, setContent] = useState(post?.content ?? '')
  useEffect(() => {
    setTitle(post?.title ?? '')
    setSummary(post?.summary ?? '')
    setContent(post?.content ?? '')
  }, [post])
  const save = useMutation({
    mutationFn: () =>
      post
        ? updateShareCenterPost(post.public_id, {
            title,
            summary,
            content,
          })
        : createShareCenterPost({ title, summary, content }),
    onSuccess: () => {
      toast.success('草稿已保存')
      onSaved()
    },
    onError: (error) => handleServerError(error),
  })
  const invalid = title.trim().length < 1 || content.trim().length < 1
  return (
    <Card>
      <CardHeader>
        <CardTitle>{post ? '编辑分享' : '新建分享'}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='space-y-2'>
          <Label>标题</Label>
          <Input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            maxLength={160}
            placeholder='例如：某平台免费额度申请方法'
          />
        </div>
        <div className='space-y-2'>
          <Label>摘要</Label>
          <Textarea
            value={summary}
            onChange={(event) => setSummary(event.target.value)}
            rows={2}
            maxLength={500}
            placeholder='用一句话说明这条分享解决什么问题'
          />
        </div>
        <div className='space-y-2'>
          <Label>正文（支持 Markdown 和安全 HTML）</Label>
          <Textarea
            value={content}
            onChange={(event) => setContent(event.target.value)}
            rows={14}
            maxLength={100000}
            className='font-mono text-sm'
            placeholder={
              '# 使用说明\n\n请写清楚来源、步骤、限制和最后验证时间。支持常用 HTML 排版，脚本和危险链接会被自动过滤。'
            }
          />
        </div>
        <div className='flex justify-end gap-2'>
          <Button variant='outline' onClick={onCancel}>
            取消
          </Button>
          <Button
            disabled={invalid || save.isPending}
            onClick={() => save.mutate()}
          >
            保存草稿
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

export function ShareCenterManage() {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['share-center-mine'],
    queryFn: getMyShareCenterPosts,
  })
  const [editing, setEditing] = useState<SharePost | null>(null)
  const [creating, setCreating] = useState(false)
  const submit = useMutation({
    mutationFn: submitShareCenterPost,
    onSuccess: () => {
      toast.success('已提交审核')
      void queryClient.invalidateQueries({ queryKey: ['share-center-mine'] })
    },
    onError: (error) => handleServerError(error),
  })
  const archive = useMutation({
    mutationFn: archiveShareCenterPost,
    onSuccess: () => {
      toast.success('已归档')
      void queryClient.invalidateQueries({ queryKey: ['share-center-mine'] })
    },
    onError: (error) => handleServerError(error),
  })
  if (query.isPending) {
    return <LoadingState />
  }
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  const posts = query.data?.data?.items ?? []
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>我的分享</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' render={<Link to='/share-center' />}>
          浏览分享中心
        </Button>
        <Button
          onClick={() => {
            setCreating(true)
            setEditing(null)
          }}
        >
          新建分享
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          {creating || editing ? (
            <SharePostEditor
              post={editing ?? undefined}
              onSaved={() => {
                setCreating(false)
                setEditing(null)
                void queryClient.invalidateQueries({
                  queryKey: ['share-center-mine'],
                })
              }}
              onCancel={() => {
                setCreating(false)
                setEditing(null)
              }}
            />
          ) : null}
          {posts.length === 0 ? (
            <EmptyState title='还没有分享' />
          ) : (
            <div className='space-y-3'>
              {posts.map((post) => (
                <Card key={post.public_id}>
                  <CardContent className='flex flex-col gap-4 p-5 md:flex-row md:items-center md:justify-between'>
                    <div className='min-w-0'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <h2 className='font-semibold'>{post.title}</h2>
                        <Badge variant='outline'>
                          {postStatusLabel(post.status)}
                        </Badge>
                      </div>
                      <p className='text-muted-foreground mt-1 text-sm'>
                        {post.summary || '无摘要'}
                      </p>
                      {post.reject_reason ? (
                        <p className='text-destructive mt-2 text-xs'>
                          驳回原因：{post.reject_reason}
                        </p>
                      ) : null}
                    </div>
                    <div className='flex shrink-0 gap-2'>
                      <Button
                        variant='outline'
                        size='sm'
                        disabled={!['draft', 'rejected'].includes(post.status)}
                        onClick={() => {
                          setEditing(post)
                          setCreating(false)
                        }}
                      >
                        编辑
                      </Button>
                      <Button
                        size='sm'
                        disabled={
                          !['draft', 'rejected'].includes(post.status) ||
                          submit.isPending
                        }
                        onClick={() => submit.mutate(post.public_id)}
                      >
                        提交审核
                      </Button>
                      <Button
                        variant='ghost'
                        size='sm'
                        disabled={
                          post.status === 'archived' || archive.isPending
                        }
                        onClick={() => archive.mutate(post.public_id)}
                      >
                        归档
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export function ShareCenterSettingsSection() {
  const config = useQuery({
    queryKey: ['share-center-config'],
    queryFn: getShareCenterConfig,
  })
  const updateConfig = useMutation({
    mutationFn: updateShareCenterConfig,
    onSuccess: () => {
      toast.success('分享中心设置已保存')
      void config.refetch()
    },
    onError: (error) => handleServerError(error),
  })

  if (config.isPending) return <LoadingState />
  if (config.isError) {
    return <ErrorState onRetry={() => void config.refetch()} />
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>分享中心访问策略</CardTitle>
      </CardHeader>
      <CardContent className='flex items-center justify-between gap-4'>
        <div>
          <p className='font-medium'>登录后才能查看分享中心</p>
          <p className='text-muted-foreground text-sm'>
            开启后，未登录用户无法打开分享中心和分享详情。分享投稿统一进入审核流程。
          </p>
        </div>
        <Switch
          checked={config.data?.data?.require_login ?? false}
          onCheckedChange={(checked) =>
            updateConfig.mutate({ require_login: checked })
          }
          disabled={updateConfig.isPending}
        />
      </CardContent>
    </Card>
  )
}

export function ShareCenterReview() {
  const queryClient = useQueryClient()
  const reviews = useQuery({
    queryKey: ['share-center-reviews'],
    queryFn: getShareCenterReviews,
  })
  const review = useMutation({
    mutationFn: ({
      id,
      action,
      reason,
    }: {
      id: string
      action: 'approve' | 'reject'
      reason?: string
    }) => reviewShareCenterPost(id, action, reason),
    onSuccess: () => {
      toast.success('审核状态已更新')
      void queryClient.invalidateQueries({ queryKey: ['share-center-reviews'] })
    },
    onError: (error) => handleServerError(error),
  })
  if (reviews.isPending) {
    return <LoadingState />
  }
  if (reviews.isError) {
    return <ErrorState onRetry={() => void reviews.refetch()} />
  }
  const posts = reviews.data?.data?.items ?? []
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>分享中心审核</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          {posts.length === 0 ? (
            <EmptyState title='暂无待审核分享' />
          ) : (
            <div className='space-y-4'>
              {posts.map((post) => (
                <Card key={post.public_id}>
                  <CardHeader>
                    <div className='flex items-start justify-between gap-3'>
                      <div>
                        <CardTitle className='text-base'>
                          {post.title}
                        </CardTitle>
                        <p className='text-muted-foreground mt-1 text-sm'>
                          {post.author_name || `用户 ${post.author_id}`}
                        </p>
                      </div>
                      <Badge>{postStatusLabel(post.status)}</Badge>
                    </div>
                  </CardHeader>
                  <CardContent>
                    <div className='bg-muted/40 max-h-48 overflow-y-auto rounded-lg p-4'>
                      <Markdown className='prose dark:prose-invert max-w-none text-sm'>
                        {post.content}
                      </Markdown>
                    </div>
                    <div className='mt-4 flex justify-end gap-2'>
                      <Button
                        variant='outline'
                        disabled={review.isPending}
                        onClick={() => {
                          const reason = window.prompt('请输入驳回原因')
                          if (reason?.trim()) {
                            review.mutate({
                              id: post.public_id,
                              action: 'reject',
                              reason,
                            })
                          }
                        }}
                      >
                        驳回
                      </Button>
                      <Button
                        disabled={review.isPending}
                        onClick={() =>
                          review.mutate({
                            id: post.public_id,
                            action: 'approve',
                          })
                        }
                      >
                        <CheckCircle2 />
                        通过并发布
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
