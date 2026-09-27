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
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout/components/public-layout'
import { PublicPageBackdrop } from '@/components/layout/components/public-page-backdrop'
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
import { useStatus } from '@/hooks/use-status'
import { handleServerError } from '@/lib/handle-server-error'
import { getModuleAccessFromStatus } from '@/lib/nav-modules'
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
    draft: 'Draft',
    pending: 'Pending review',
    published: 'Published',
    rejected: 'Needs changes',
    archived: 'Archived',
  }[status]
}

const SHARE_FILTERS: { id: ShareFilter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'usable', label: 'Still usable' },
  { id: 'observing', label: 'Needs observation' },
  { id: 'invalid', label: 'May be unavailable' },
]

export function SharePostCard({ post }: { post: SharePost }) {
  const { t, i18n } = useTranslation()
  const state = postState(post)
  const summary = voteSummary(post)
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
                    {post.author_name || t('Sharer')}
                  </span>
                  <span aria-hidden>·</span>
                  <span className='inline-flex items-center gap-1'>
                    <CalendarClock className='size-3.5' />
                    {formatPublishedAt(
                      post.published_at || post.updated_at,
                      i18n.language,
                      t('Not published yet')
                    )}
                  </span>
                </div>
              </div>
            </div>
            <Badge
              variant={badgeVariant}
              className='shrink-0 whitespace-nowrap'
            >
              {t(summary.key, summary.values)}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className='flex flex-1 flex-col justify-between gap-4'>
          <p className='text-muted-foreground line-clamp-3 text-sm leading-6'>
            {post.summary ||
              post.content.replaceAll(/[#*_`>]/g, '').slice(0, 180)}
          </p>
          <div className='text-muted-foreground flex items-center justify-between border-t pt-3 text-xs'>
            <span>{t('Availability feedback')}</span>
            <span className='text-foreground font-medium tabular-nums'>
              {t('{{usable}} usable · {{unavailable}} unavailable', {
                usable: post.usable_votes,
                unavailable: post.invalid_votes,
              })}
            </span>
          </div>
        </CardContent>
      </Card>
    </Link>
  )
}

export function ShareCenterBrowser() {
  const { t } = useTranslation()
  const { status } = useStatus()
  // Hide the cross-link when an admin turned relay sharing off (its route would 403).
  const relayShareEnabled = getModuleAccessFromStatus(
    status as Record<string, unknown> | null,
    'relayShare'
  ).enabled
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
      <PublicPageBackdrop>
        <PageTransition className='mx-auto w-full max-w-[1280px] space-y-8 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <section className='flex flex-wrap items-end justify-between gap-5'>
            <div className='max-w-3xl'>
              <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
                {t('Community library')}
              </p>
              <h1 className='mt-2 text-[clamp(2rem,4vw,3rem)] leading-[1.1] font-bold tracking-tight'>
                {t('Share center')}
              </h1>
              <p className='text-muted-foreground mt-3 max-w-2xl text-sm leading-6 sm:text-base'>
                {t(
                  'Share useful, verified information to help the next person. This space collects free offers, promotions, tutorials and usage notes; availability is based on community feedback and admin status.'
                )}
              </p>
            </div>
            <div className='flex shrink-0 flex-wrap gap-2'>
              {relayShareEnabled ? (
                <Button variant='outline' render={<Link to='/relay-share' />}>
                  {t('Relay sharing')}
                </Button>
              ) : null}
              <Button render={<Link to='/share-center/manage' />}>
                <FileText />
                {t('My shares')}
              </Button>
            </div>
          </section>

          <section className='grid gap-3 sm:grid-cols-3'>
            {[
              {
                label: t('Published shares'),
                value: posts.length,
                icon: FileText,
              },
              {
                label: t('Mostly usable'),
                value: usableCount,
                icon: CircleCheck,
              },
              {
                label: t('Availability feedback'),
                value: voteCount,
                icon: ShieldCheck,
              },
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
                    placeholder={t('Search titles or summaries')}
                    className='h-10 pl-9'
                  />
                </div>
                <Button onClick={() => setSubmittedKeyword(keyword)}>
                  {t('Search shares')}
                </Button>
              </div>
              <div
                className='flex flex-wrap items-center gap-2'
                role='tablist'
                aria-label={t('Share status')}
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
                    {t(item.label)}
                  </button>
                ))}
              </div>
            </CardContent>
          </Card>

          <section className='space-y-4'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <h2 className='text-lg font-semibold tracking-tight'>
                  {t('Latest shares')}
                </h2>
                <p className='text-muted-foreground mt-1 text-sm'>
                  {t('Browse by update time, without an overall ranking.')}
                </p>
              </div>
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('{{count}} results', { count: visiblePosts.length })}
              </span>
            </div>
            {visiblePosts.length === 0 ? (
              <div className='bg-card/70 rounded-xl border border-dashed px-6 py-12'>
                <EmptyState title={t('No shares match these filters')} />
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
      </PublicPageBackdrop>
    </PublicLayout>
  )
}

export function ShareCenterPostDetail({ publicID }: { publicID: string }) {
  const { t, i18n } = useTranslation()
  const auth = useAuthStore((state) => state.auth)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['share-center-post', publicID],
    queryFn: () => getShareCenterPost(publicID),
  })
  const vote = useMutation({
    mutationFn: (value: 1 | -1) => voteShareCenterPost(publicID, value),
    onSuccess: () => {
      toast.success(t('Thanks for your feedback'))
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
  const summary = voteSummary(post)
  return (
    <PublicLayout showMainContainer={false}>
      <PublicPageBackdrop>
        <PageTransition className='mx-auto w-full max-w-[1180px] space-y-6 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div className='flex flex-wrap items-end justify-between gap-4'>
            <div className='min-w-0'>
              <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
                {t('Community share · Details')}
              </p>
              <h1 className='mt-2 text-[clamp(1.75rem,4vw,2.75rem)] leading-[1.12] font-bold tracking-tight'>
                {post.title}
              </h1>
              <div className='text-muted-foreground mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm'>
                <span className='inline-flex items-center gap-1.5'>
                  <UserRound className='size-4' />
                  {post.author_name || t('Sharer')}
                </span>
                <span aria-hidden>·</span>
                <span className='inline-flex items-center gap-1.5'>
                  <CalendarClock className='size-4' />
                  {t('Updated {{date}}', {
                    date: formatPublishedAt(
                      post.updated_at || post.published_at,
                      i18n.language,
                      t('Not published yet')
                    ),
                  })}
                </span>
              </div>
            </div>
            <Button variant='outline' render={<Link to='/share-center' />}>
              {t('Back to share center')}
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
                  <CardTitle className='text-base'>
                    {t('Share information')}
                  </CardTitle>
                </CardHeader>
                <CardContent className='space-y-4 text-sm'>
                  <div>
                    <span className='text-muted-foreground'>
                      {t('Current assessment')}
                    </span>
                    <p className='mt-1 font-medium'>
                      {t(summary.key, summary.values)}
                    </p>
                  </div>
                  <div>
                    <span className='text-muted-foreground'>
                      {t('Published at')}
                    </span>
                    <p className='mt-1 font-medium'>
                      {formatPublishedAt(
                        post.published_at,
                        i18n.language,
                        t('Not published yet')
                      )}
                    </p>
                  </div>
                  <div className='bg-muted/40 rounded-lg p-3 text-xs leading-5'>
                    <Clock3 className='text-primary mb-1 size-4' />
                    {t(
                      'Status changes with community feedback; votes are only troubleshooting signals.'
                    )}
                  </div>
                </CardContent>
              </Card>
              <Card className='bg-card/90'>
                <CardHeader>
                  <CardTitle className='text-base'>
                    {t('Is it still usable?')}
                  </CardTitle>
                </CardHeader>
                <CardContent className='space-y-3'>
                  <Button
                    className='w-full'
                    disabled={!auth?.user || vote.isPending}
                    onClick={() => vote.mutate(1)}
                  >
                    <ThumbsUp />
                    {t('Still usable')}
                  </Button>
                  <Button
                    variant='outline'
                    className='w-full'
                    disabled={!auth?.user || vote.isPending}
                    onClick={() => vote.mutate(-1)}
                  >
                    <ThumbsDown />
                    {t('Unavailable')}
                  </Button>
                  {!auth?.user ? (
                    <p className='text-muted-foreground text-xs'>
                      {t('Sign in to vote.')}
                    </p>
                  ) : null}
                  {state === 'usable' ? (
                    <p className='text-muted-foreground text-xs'>
                      {t('Most feedback says this is currently available.')}
                    </p>
                  ) : null}
                </CardContent>
              </Card>
            </aside>
          </div>
        </PageTransition>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}

type EditorProps = {
  post?: SharePost
  onSaved: () => void
  onCancel: () => void
}

export function SharePostEditor({ post, onSaved, onCancel }: EditorProps) {
  const { t } = useTranslation()
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
      toast.success(t('Draft saved'))
      onSaved()
    },
    onError: (error) => handleServerError(error),
  })
  const invalid = title.trim().length < 1 || content.trim().length < 1
  return (
    <Card>
      <CardHeader>
        <CardTitle>{post ? t('Edit share') : t('New share')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Title')}</Label>
          <Input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            maxLength={160}
            placeholder={t('e.g. How to apply for a platform free quota')}
          />
        </div>
        <div className='space-y-2'>
          <Label>{t('Summary')}</Label>
          <Textarea
            value={summary}
            onChange={(event) => setSummary(event.target.value)}
            rows={2}
            maxLength={500}
            placeholder={t('Briefly describe what this share helps with.')}
          />
        </div>
        <div className='space-y-2'>
          <Label>{t('Content (Markdown and safe HTML supported)')}</Label>
          <Textarea
            value={content}
            onChange={(event) => setContent(event.target.value)}
            rows={14}
            maxLength={100000}
            className='font-mono text-sm'
            placeholder={
              '# Instructions\n\nInclude the source, steps, limitations and last verification date. Common HTML formatting is supported; scripts and unsafe links are filtered.'
            }
          />
        </div>
        <div className='flex justify-end gap-2'>
          <Button variant='outline' onClick={onCancel}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={invalid || save.isPending}
            onClick={() => save.mutate()}
          >
            {t('Save draft')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

export function ShareCenterManage() {
  const { t } = useTranslation()
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
      toast.success(t('Submitted for review.'))
      void queryClient.invalidateQueries({ queryKey: ['share-center-mine'] })
    },
    onError: (error) => handleServerError(error),
  })
  const archive = useMutation({
    mutationFn: archiveShareCenterPost,
    onSuccess: () => {
      toast.success(t('Archived.'))
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
      <SectionPageLayout.Title>{t('My shares')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' render={<Link to='/share-center' />}>
          {t('Browse share center')}
        </Button>
        <Button
          onClick={() => {
            setCreating(true)
            setEditing(null)
          }}
        >
          {t('Create share')}
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
            <EmptyState title={t('No shares yet.')} />
          ) : (
            <div className='space-y-3'>
              {posts.map((post) => (
                <Card key={post.public_id}>
                  <CardContent className='flex flex-col gap-4 p-5 md:flex-row md:items-center md:justify-between'>
                    <div className='min-w-0'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <h2 className='font-semibold'>{post.title}</h2>
                        <Badge variant='outline'>
                          {t(postStatusLabel(post.status))}
                        </Badge>
                      </div>
                      <p className='text-muted-foreground mt-1 text-sm'>
                        {post.summary || t('No summary')}
                      </p>
                      {post.reject_reason ? (
                        <p className='text-destructive mt-2 text-xs'>
                          {t('Rejection reason: {{reason}}', {
                            reason: post.reject_reason,
                          })}
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
                        {t('Edit')}
                      </Button>
                      <Button
                        size='sm'
                        disabled={
                          !['draft', 'rejected'].includes(post.status) ||
                          submit.isPending
                        }
                        onClick={() => submit.mutate(post.public_id)}
                      >
                        {t('Submit for review')}
                      </Button>
                      <Button
                        variant='ghost'
                        size='sm'
                        disabled={
                          post.status === 'archived' || archive.isPending
                        }
                        onClick={() => archive.mutate(post.public_id)}
                      >
                        {t('Archive')}
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
  const { t } = useTranslation()
  const config = useQuery({
    queryKey: ['share-center-config'],
    queryFn: getShareCenterConfig,
  })
  const updateConfig = useMutation({
    mutationFn: updateShareCenterConfig,
    onSuccess: () => {
      toast.success(t('Share center settings saved.'))
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
        <CardTitle className='text-base'>
          {t('Share center access policy')}
        </CardTitle>
      </CardHeader>
      <CardContent className='flex items-center justify-between gap-4'>
        <div>
          <p className='font-medium'>
            {t('Require sign-in to view share center')}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'When enabled, signed-out visitors cannot open the share center or details. New submissions always go through review.'
            )}
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
  const { t } = useTranslation()
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
      toast.success(t('Review status updated.'))
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
      <SectionPageLayout.Title>
        {t('Share center review')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          {posts.length === 0 ? (
            <EmptyState title={t('No shares are awaiting review.')} />
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
                          {post.author_name ||
                            t('User {{id}}', { id: post.author_id })}
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
                          const reason = window.prompt(
                            t('Enter a reason for rejection')
                          )
                          if (reason?.trim()) {
                            review.mutate({
                              id: post.public_id,
                              action: 'reject',
                              reason,
                            })
                          }
                        }}
                      >
                        {t('Reject')}
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
                        {t('Approve and publish')}
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
