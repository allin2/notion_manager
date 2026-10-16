import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { AccountInfo } from '../types'
import { deleteAccount, openProxy, unrestAccount } from '../api'
import { IconExternalLink, IconMore, IconRotate, IconTrash } from './Icons'

interface Props {
  account: AccountInfo
  onChanged: () => void
}

// AccountMenu renders the per-card 3-dot dropdown. Stop propagation on the
// trigger so clicking it doesn't bubble up to the card's "open proxy"
// click handler.
export function AccountMenu({ account, onChanged }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [unresting, setUnresting] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDocClick = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false)
        setConfirming(false)
      }
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        setConfirming(false)
      }
    }
    window.addEventListener('mousedown', onDocClick)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('mousedown', onDocClick)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  const onOpenProxy = (e: React.MouseEvent) => {
    e.stopPropagation()
    openProxy(account.account_id)
    setOpen(false)
  }

  const onDelete = async (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!confirming) {
      setConfirming(true)
      return
    }
    setDeleting(true)
    try {
      await deleteAccount(account.account_id)
      onChanged()
    } catch (err) {
      console.error('delete account failed', err)
    } finally {
      setDeleting(false)
      setOpen(false)
      setConfirming(false)
    }
  }

  // Unrest appears only while the account carries active rests (recorded
  // by the failure classifier). It returns the account — and every rested
  // (account, model) pair — to rotation without waiting for expiry.
  const onUnrest = async (e: React.MouseEvent) => {
    e.stopPropagation()
    setUnresting(true)
    try {
      await unrestAccount(account.email)
      onChanged()
    } catch (err) {
      console.error('unrest account failed', err)
      alert(t('menu.unrest_failed'))
    } finally {
      setUnresting(false)
      setOpen(false)
    }
  }

  return (
    <div ref={wrapRef} className="relative" onClick={(e) => e.stopPropagation()}>
      <button
        onClick={(e) => {
          e.stopPropagation()
          setOpen((v) => !v)
        }}
        className="w-6 h-6 rounded hover:bg-white/[.08] text-text-secondary hover:text-text-primary flex items-center justify-center bg-transparent border-none cursor-pointer transition-colors"
        title={t('menu.more_actions')}
      >
        <IconMore size={14} />
      </button>
      {open && (
        <div
          className="absolute right-0 top-7 z-30 w-44 bg-bg-secondary border border-border rounded-md shadow-xl shadow-black/40 py-1"
          onClick={(e) => e.stopPropagation()}
        >
          <MenuItem onClick={onOpenProxy} icon={<IconExternalLink size={13} />} label={t('menu.open_proxy')} />
          {!!account.rests?.length && (
            <MenuItem
              onClick={onUnrest}
              disabled={unresting}
              icon={<IconRotate size={13} />}
              label={t('menu.unrest')}
            />
          )}
          <div className="border-t border-border my-1" />
          <MenuItem
            onClick={onDelete}
            danger
            disabled={deleting}
            icon={<IconTrash size={13} />}
            label={confirming ? t('menu.confirm_delete') : t('menu.delete_account')}
          />
        </div>
      )}
    </div>
  )
}

function MenuItem({
  onClick,
  icon,
  label,
  disabled,
  danger,
}: {
  onClick: (e: React.MouseEvent) => void
  icon: React.ReactNode
  label: string
  disabled?: boolean
  danger?: boolean
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`w-full flex items-center gap-2 px-3 py-1.5 text-[12px] text-left bg-transparent border-none cursor-pointer transition-colors ${
        danger
          ? 'text-err hover:bg-err/10'
          : 'text-text-secondary hover:text-text-primary hover:bg-white/[.05]'
      } disabled:opacity-40 disabled:cursor-not-allowed`}
    >
      <span className="shrink-0">{icon}</span>
      <span>{label}</span>
    </button>
  )
}
