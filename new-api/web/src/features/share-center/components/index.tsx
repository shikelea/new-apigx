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
  CheckCircle2,
  FileText,
  Search,
  ThumbsDown,
  ThumbsUp,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Markdown } from '@/components/ui/markdown'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
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

function voteSummary(post: SharePost) {
  const total = post.usable_votes + post.invalid_votes
  if (total === 0) return '暂无有效性样本'
  if (post.usable_votes > post.invalid_votes) {
    return `仍可用 · ${post.usable_votes}/${total}`
  }
  if (post.invalid_votes > post.usable_votes) {
    return `可能失效 · ${post.invalid_votes}/${total}`
  }
  return `状态待观察 · ${total} 票`
}

export function SharePostCard({ post }: { post: SharePost }) {
  return (
    <Link
      to='/share-center/$publicID'
      params={{ publicID: post.public_id }}
      className='group block'
    >
      <Card className='h-full transition-transform duration-200 group-hover:-translate-y-0.5 group-hover:shadow-md'>
        <CardHeader className='gap-3'>
          <div className='flex items-start justify-between gap-3'>
            <CardTitle className='line-clamp-2 text-lg'>{post.title}</CardTitle>
            <Badge
              variant={
                post.usable_votes >= post.invalid_votes
                  ? 'secondary'
                  : 'outline'
              }
            >
              {voteSummary(post)}
            </Badge>
          </div>
          <div className='text-muted-foreground flex flex-wrap gap-2 text-xs'>
            <span>{post.author_name || '分享者'}</span>
          </div>
        </CardHeader>
        <CardContent>
          <p className='text-muted-foreground line-clamp-3 text-sm leading-6'>
            {post.summary ||
              post.content.replaceAll(/[#*_`>]/g, '').slice(0, 180)}
          </p>
        </CardContent>
      </Card>
    </Link>
  )
}

export function ShareCenterBrowser() {
  const [keyword, setKeyword] = useState('')
  const [submittedKeyword, setSubmittedKeyword] = useState('')
  const query = useQuery({
    queryKey: ['share-center-posts', submittedKeyword],
    queryFn: () => getShareCenterPosts(submittedKeyword),
  })

  if (query.isPending) {
    return <LoadingState />
  }
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />

  const posts = query.data?.data?.items ?? []
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>分享中心</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' render={<Link to='/share-center/manage' />}>
          <FileText />
          我的分享
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <section className='from-primary/10 via-background to-background rounded-2xl border bg-gradient-to-br p-6 md:p-8'>
            <p className='text-primary mb-2 text-xs font-semibold tracking-[0.18em] uppercase'>
              社区分享
            </p>
            <h1 className='max-w-2xl text-3xl font-semibold tracking-tight md:text-4xl'>
              分享真实可用的信息，让下一位少走弯路。
            </h1>
            <p className='text-muted-foreground mt-3 max-w-2xl text-sm leading-6 md:text-base'>
              这里仅收录文字和 Markdown
              内容，适合分享白嫖渠道说明、福利羊毛、教程与使用经验。内容是否仍可用，由社区投票和管理员状态共同呈现。
            </p>
          </section>
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
                className='pl-9'
              />
            </div>
            <Button onClick={() => setSubmittedKeyword(keyword)}>搜索</Button>
          </div>
          {posts.length === 0 ? (
            <EmptyState title='暂无已发布分享' />
          ) : (
            <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
              {posts.map((post) => (
                <SharePostCard key={post.public_id} post={post} />
              ))}
            </div>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
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
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{post.title}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' render={<Link to='/share-center' />}>
          返回分享中心
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto grid max-w-5xl gap-6 lg:grid-cols-[minmax(0,1fr)_260px]'>
          <Card>
            <CardContent className='p-6 md:p-8'>
              <Markdown className='prose dark:prose-invert max-w-none'>
                {post.content}
              </Markdown>
            </CardContent>
          </Card>
          <aside className='space-y-4'>
            <Card>
              <CardHeader>
                <CardTitle className='text-base'>分享信息</CardTitle>
              </CardHeader>
              <CardContent className='space-y-3 text-sm'>
                <div>
                  <span className='text-muted-foreground'>分享者</span>
                  <p className='font-medium'>
                    {post.author_name || '分享者'}
                  </p>
                </div>
                <div>
                  <span className='text-muted-foreground'>当前判断</span>
                  <p className='font-medium'>{voteSummary(post)}</p>
                </div>
              </CardContent>
            </Card>
            <Card>
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
              </CardContent>
            </Card>
          </aside>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
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
  if (config.isError) return <ErrorState onRetry={() => void config.refetch()} />

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
  if (reviews.isError) return <ErrorState onRetry={() => void reviews.refetch()} />
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
