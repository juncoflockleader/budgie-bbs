import { FormEvent, useEffect, useState } from 'react'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'
import * as api from '../api/client'
import type { AccountRegistration, AccountRegistrationSettings, PasswordRecoveryRequest, Post, UserLoginACLBundle, UserPersonalFile, UserPrivateProfile, UserProfile, UserSignatureBundle } from '../api/types'

interface Props {
  token: string
  username: string
  isOwnProfile: boolean
  currentUserRole: string
  onBack: () => void
  onOpenAuthorPosts: (username: string) => void
}

const TL_LABEL = ['TL0', 'TL1', 'TL2', 'TL3', 'TL4']

type PrivateProfileDraft = Omit<UserPrivateProfile, 'userId' | 'updatedAt'>

function emptyPrivateDraft(): PrivateProfileDraft {
  return {
    realName: '',
    realEmail: '',
    registrationEmail: '',
    address: '',
    phone: '',
    mobile: '',
    birthday: '',
    school: '',
    contactNote: '',
  }
}

function privateDraftFromProfile(profile: UserPrivateProfile): PrivateProfileDraft {
  return {
    realName: profile.realName,
    realEmail: profile.realEmail,
    registrationEmail: profile.registrationEmail,
    address: profile.address,
    phone: profile.phone,
    mobile: profile.mobile,
    birthday: profile.birthday,
    school: profile.school,
    contactNote: profile.contactNote,
  }
}

function homepageHref(homepage: string): string {
  const value = homepage.trim()
  if (!value) return ''
  const candidate = /^https?:\/\//i.test(value) ? value : `https://${value}`
  try {
    const url = new URL(candidate)
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.toString() : ''
  } catch {
    return ''
  }
}

export function UserProfilePage({ token, username, isOwnProfile, currentUserRole, onBack, onOpenAuthorPosts }: Props) {
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [recentPosts, setRecentPosts] = useState<Post[]>([])
  const [loading, setLoading] = useState(true)
  const [loadingPosts, setLoadingPosts] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [editMode, setEditMode] = useState(false)
  const [saving, setSaving] = useState(false)
  const [displayName, setDisplayName] = useState('')
  const [title, setTitle] = useState('')
  const [bio, setBio] = useState('')
  const [avatar, setAvatar] = useState('')
  const [signature, setSignature] = useState('')
  const [plan, setPlan] = useState('')
  const [homepage, setHomepage] = useState('')
  const [saveError, setSaveError] = useState<string | null>(null)
  const [relationNotice, setRelationNotice] = useState<string | null>(null)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [passwordSaving, setPasswordSaving] = useState(false)
  const [passwordNotice, setPasswordNotice] = useState<string | null>(null)
  const [deactivatePassword, setDeactivatePassword] = useState('')
  const [deactivateReason, setDeactivateReason] = useState('')
  const [deactivating, setDeactivating] = useState(false)
  const [deactivateNotice, setDeactivateNotice] = useState<string | null>(null)
  const [signatureBundle, setSignatureBundle] = useState<UserSignatureBundle | null>(null)
  const [signatureDrafts, setSignatureDrafts] = useState<Record<string, { label: string; body: string; active: boolean; position: number }>>({})
  const [signatureNotice, setSignatureNotice] = useState<string | null>(null)
  const [signatureSaving, setSignatureSaving] = useState<string | null>(null)
  const [newSignatureLabel, setNewSignatureLabel] = useState('')
  const [newSignatureBody, setNewSignatureBody] = useState('')
  const [loginACL, setLoginACL] = useState<UserLoginACLBundle | null>(null)
  const [loginACLDrafts, setLoginACLDrafts] = useState<Record<string, { pattern: string; note: string; active: boolean; position: number }>>({})
  const [loginACLNotice, setLoginACLNotice] = useState<string | null>(null)
  const [loginACLSaving, setLoginACLSaving] = useState<string | null>(null)
  const [newLoginACLPattern, setNewLoginACLPattern] = useState('')
  const [newLoginACLNote, setNewLoginACLNote] = useState('')
  const [privateProfile, setPrivateProfile] = useState<UserPrivateProfile | null>(null)
  const [privateDraft, setPrivateDraft] = useState<PrivateProfileDraft>(emptyPrivateDraft)
  const [privateSaving, setPrivateSaving] = useState(false)
  const [privateNotice, setPrivateNotice] = useState<string | null>(null)
  const [personalFiles, setPersonalFiles] = useState<UserPersonalFile[]>([])
  const [personalFileDrafts, setPersonalFileDrafts] = useState<Record<string, { body: string; public: boolean }>>({})
  const [personalFileNotice, setPersonalFileNotice] = useState<string | null>(null)
  const [personalFileSaving, setPersonalFileSaving] = useState<string | null>(null)
  const [newPersonalFileName, setNewPersonalFileName] = useState('')
  const [newPersonalFileBody, setNewPersonalFileBody] = useState('')
  const [newPersonalFilePublic, setNewPersonalFilePublic] = useState(true)
  const [registrationSettings, setRegistrationSettings] = useState<AccountRegistrationSettings | null>(null)
  const [pendingRegistrations, setPendingRegistrations] = useState<AccountRegistration[]>([])
  const [passwordRecoveryRequests, setPasswordRecoveryRequests] = useState<PasswordRecoveryRequest[]>([])
  const [registrationNotice, setRegistrationNotice] = useState<string | null>(null)
  const [registrationSaving, setRegistrationSaving] = useState(false)
  const [transferName, setTransferName] = useState('')
  const [transferNotice, setTransferNotice] = useState<string | null>(null)
  const [transferSaving, setTransferSaving] = useState(false)
  const [deleteReason, setDeleteReason] = useState('')
  const [deleteNotice, setDeleteNotice] = useState<string | null>(null)
  const [deleteSaving, setDeleteSaving] = useState(false)
  const [twoFAStatus, setTwoFAStatus] = useState<{ totpEnrolled: boolean; emailEnrolled: boolean } | null>(null)
  const [totpEnroll, setTotpEnroll] = useState<{ secret: string; otpauthUri: string; qr: string } | null>(null)
  const [totpCode, setTotpCode] = useState('')
  const [twoFANotice, setTwoFANotice] = useState<string | null>(null)

  async function loadTwoFA() {
    if (!isOwnProfile) return
    const res = await api.getTwoFactorStatus(token)
    if (res.data) setTwoFAStatus(res.data)
  }
  async function beginTOTP() {
    setTwoFANotice(null)
    const res = await api.initTOTP(token)
    if (res.error) { setTwoFANotice(res.error.message); return }
    setTotpEnroll(res.data ?? null)
    setTotpCode('')
  }
  async function confirmTOTPCode() {
    const res = await api.confirmTOTP(token, totpCode.trim())
    if (res.error) { setTwoFANotice('Invalid code — try again.'); return }
    setTotpEnroll(null)
    setTotpCode('')
    setTwoFANotice('Authenticator app enrolled.')
    await loadTwoFA()
  }
  async function removeTOTP() {
    const res = await api.disableTOTP(token)
    if (res.error) { setTwoFANotice(res.error.message); return }
    setTwoFANotice('Authenticator app removed.')
    await loadTwoFA()
  }
  async function toggleEmail2FA(enable: boolean) {
    const res = enable ? await api.enableEmailTwoFactor(token) : await api.disableEmailTwoFactor(token)
    if (res.error) { setTwoFANotice(res.error.message); return }
    setTwoFANotice(enable ? 'Email codes enabled.' : 'Email codes disabled.')
    await loadTwoFA()
  }

  async function loadProfile() {
    setLoading(true)
    setError(null)
    const res = await api.getUserProfile(token, username)
    setLoading(false)
    if (res.error) {
      setError(res.error.message)
      setProfile(null)
      return
    }
    if (res.data) {
      setProfile(res.data)
      setDisplayName(res.data.displayName)
      setTitle(res.data.title)
      setBio(res.data.bio)
      setAvatar(res.data.avatar)
      setSignature(res.data.signature)
      setPlan(res.data.plan)
      setHomepage(res.data.homepage)
      setTransferName(res.data.name)
    }
  }

  async function loadPosts() {
    setLoadingPosts(true)
    const res = await api.listUserPosts(token, username, 20, 0)
    setLoadingPosts(false)
    if (res.error) return
    setRecentPosts(res.data ?? [])
  }

  async function loadSignatures() {
    if (!isOwnProfile) {
      setSignatureBundle(null)
      setSignatureDrafts({})
      return
    }
    const res = await api.listMySignatures(token)
    if (res.error) {
      setSignatureNotice(res.error.message)
      return
    }
    const bundle = res.data ?? {
      signatures: [],
      settings: { userId: '', selectedSignatureId: '', randomEnabled: false, updatedAt: 0 },
      maxCount: 8,
    }
    setSignatureBundle(bundle)
    setSignatureDrafts(Object.fromEntries(bundle.signatures.map(sig => [sig.id, {
      label: sig.label,
      body: sig.body,
      active: sig.active,
      position: sig.position,
    }])))
  }

  async function loadLoginACL() {
    if (!isOwnProfile) {
      setLoginACL(null)
      setLoginACLDrafts({})
      return
    }
    const res = await api.listMyLoginACL(token)
    if (res.error) {
      setLoginACLNotice(res.error.message)
      return
    }
    const bundle = res.data ?? {
      rules: [],
      settings: { userId: '', enabled: false, updatedAt: 0 },
      allowed: true,
    }
    setLoginACL(bundle)
    setLoginACLDrafts(Object.fromEntries(bundle.rules.map(rule => [rule.id, {
      pattern: rule.pattern,
      note: rule.note,
      active: rule.active,
      position: rule.position,
    }])))
  }

  async function loadPrivateProfile() {
    if (!isOwnProfile) {
      setPrivateProfile(null)
      setPrivateDraft(emptyPrivateDraft())
      return
    }
    const res = await api.getMyPrivateProfile(token)
    if (res.error) {
      setPrivateNotice(res.error.message)
      return
    }
    const data = res.data ?? { userId: '', updatedAt: 0, ...emptyPrivateDraft() }
    setPrivateProfile(data)
    setPrivateDraft(privateDraftFromProfile(data))
  }

  async function loadPersonalFiles() {
    const res = isOwnProfile ? await api.listMyPersonalFiles(token) : await api.listUserPersonalFiles(token, username)
    if (res.error) {
      if (isOwnProfile) setPersonalFileNotice(res.error.message)
      setPersonalFiles([])
      setPersonalFileDrafts({})
      return
    }
    const files = res.data ?? []
    setPersonalFiles(files)
    setPersonalFileDrafts(Object.fromEntries(files.map(file => [file.name, { body: file.body, public: file.public }])))
  }

  async function loadRegistrationAdmin() {
    if (!isOwnProfile || currentUserRole !== 'admin') {
      setRegistrationSettings(null)
      setPendingRegistrations([])
      setPasswordRecoveryRequests([])
      return
    }
    const [settingsRes, pendingRes, recoveryRes] = await Promise.all([
      api.getAccountRegistrationSettings(token),
      api.listAccountRegistrations(token, 'pending', 50, 0),
      api.listPasswordRecoveryRequests(token, 'pending', 50, 0),
    ])
    if (settingsRes.error) {
      setRegistrationNotice(settingsRes.error.message)
    } else {
      setRegistrationSettings(settingsRes.data ?? null)
    }
    if (pendingRes.error) {
      setRegistrationNotice(pendingRes.error.message)
    } else {
      setPendingRegistrations(pendingRes.data ?? [])
    }
    if (recoveryRes.error) {
      setRegistrationNotice(recoveryRes.error.message)
    } else {
      setPasswordRecoveryRequests(recoveryRes.data ?? [])
    }
  }

  useEffect(() => {
    setRecentPosts([])
    loadProfile()
    loadPosts()
    loadSignatures()
    loadLoginACL()
    loadPrivateProfile()
    loadPersonalFiles()
    loadRegistrationAdmin()
    loadTwoFA()
  }, [token, username, isOwnProfile, currentUserRole])

  async function submitProfile(event: FormEvent) {
    event.preventDefault()
    if (!profile) return

    setSaving(true)
    setSaveError(null)
    const res = await api.updateMyProfile(token, {
      displayName: displayName.trim(),
      title: title.trim(),
      bio: bio.trim(),
      avatar: avatar.trim(),
      signature: signature.trim(),
      plan: plan.trim(),
      homepage: homepage.trim(),
    })

    setSaving(false)
    if (res.error) {
      setSaveError(res.error.message)
      return
    }
    setProfile(prev => prev ? {
      ...prev,
      displayName: displayName.trim() || profile.name,
      title: title.trim(),
      bio: bio.trim(),
      avatar: avatar.trim(),
      signature: signature.trim(),
      plan: plan.trim(),
      homepage: homepage.trim(),
    } : prev)
    setEditMode(false)
  }

  async function setRelation(kind: 'friend' | 'ignore', active: boolean) {
    const note = active && kind === 'friend' ? prompt('Friend note:', '') ?? '' : ''
    const res = await api.setUserRelationship(token, username, kind, active, note)
    if (res.error) {
      setRelationNotice(res.error.message)
      return
    }
    setRelationNotice(active ? `${kind === 'friend' ? 'Friend' : 'Ignore'} saved.` : `${kind === 'friend' ? 'Friend' : 'Ignore'} removed.`)
  }

  async function blessProfile() {
    if (!profile) return
    const message = prompt('Blessing message:', '') ?? ''
    const res = await api.blessUser(token, profile.name, message)
    if (res.error) {
      setRelationNotice(res.error.message)
      return
    }
    setRelationNotice('Blessing sent.')
  }

  async function submitPassword(event: FormEvent) {
    event.preventDefault()
    setPasswordNotice(null)
    if (newPassword !== confirmPassword) {
      setPasswordNotice('New passwords do not match.')
      return
    }
    setPasswordSaving(true)
    const res = await api.changeMyPassword(token, { currentPassword, newPassword })
    setPasswordSaving(false)
    if (res.error) {
      setPasswordNotice(res.error.message)
      return
    }
    setCurrentPassword('')
    setNewPassword('')
    setConfirmPassword('')
    setPasswordNotice('Password updated.')
  }

  async function submitDeactivate(event: FormEvent) {
    event.preventDefault()
    setDeactivateNotice(null)
    if (!confirm('Close this account? You will not be able to sign in again.')) return
    setDeactivating(true)
    const res = await api.deactivateMyAccount(token, {
      password: deactivatePassword,
      reason: deactivateReason.trim(),
    })
    setDeactivating(false)
    if (res.error) {
      setDeactivateNotice(res.error.message)
      return
    }
    setDeactivatePassword('')
    setDeactivateReason('')
    setDeactivateNotice('Account closed.')
  }

  async function createSignature(event: FormEvent) {
    event.preventDefault()
    setSignatureNotice(null)
    setSignatureSaving('new')
    const res = await api.createMySignature(token, {
      label: newSignatureLabel.trim(),
      body: newSignatureBody.trim(),
      active: true,
    })
    setSignatureSaving(null)
    if (res.error) {
      setSignatureNotice(res.error.message)
      return
    }
    setNewSignatureLabel('')
    setNewSignatureBody('')
    setSignatureNotice('Signature saved.')
    await loadSignatures()
    await loadProfile()
  }

  async function saveSignature(signatureId: string) {
    const draft = signatureDrafts[signatureId]
    if (!draft) return
    setSignatureNotice(null)
    setSignatureSaving(signatureId)
    const res = await api.updateMySignature(token, signatureId, {
      label: draft.label.trim(),
      body: draft.body.trim(),
      position: draft.position,
      active: draft.active,
    })
    setSignatureSaving(null)
    if (res.error) {
      setSignatureNotice(res.error.message)
      return
    }
    setSignatureNotice('Signature updated.')
    await loadSignatures()
    await loadProfile()
  }

  async function deleteSignature(signatureId: string) {
    if (!confirm('Delete this signature?')) return
    setSignatureNotice(null)
    setSignatureSaving(signatureId)
    const res = await api.deleteMySignature(token, signatureId)
    setSignatureSaving(null)
    if (res.error) {
      setSignatureNotice(res.error.message)
      return
    }
    setSignatureNotice('Signature deleted.')
    await loadSignatures()
    await loadProfile()
  }

  async function setSignatureSelection(selectedSignatureId: string, randomEnabled = signatureBundle?.settings.randomEnabled ?? false) {
    setSignatureNotice(null)
    const res = await api.setMySignatureSettings(token, { selectedSignatureId, randomEnabled })
    if (res.error) {
      setSignatureNotice(res.error.message)
      return
    }
    setSignatureNotice('Signature settings updated.')
    await loadSignatures()
    await loadProfile()
  }

  async function recountSignatures() {
    setSignatureNotice(null)
    setSignatureSaving('recount')
    const res = await api.recountMySignatures(token)
    setSignatureSaving(null)
    if (res.error) {
      setSignatureNotice(res.error.message)
      return
    }
    setSignatureNotice(res.data ? `Signature count refreshed: ${res.data.count}/${res.data.activeCount} active.` : 'Signature count refreshed.')
    await loadSignatures()
    await loadProfile()
  }

  async function createLoginACLRule(event: FormEvent) {
    event.preventDefault()
    setLoginACLNotice(null)
    setLoginACLSaving('new')
    const res = await api.createMyLoginACLRule(token, {
      pattern: newLoginACLPattern.trim(),
      note: newLoginACLNote.trim(),
      active: true,
    })
    setLoginACLSaving(null)
    if (res.error) {
      setLoginACLNotice(res.error.message)
      return
    }
    setNewLoginACLPattern('')
    setNewLoginACLNote('')
    setLoginACLNotice('Login rule saved.')
    await loadLoginACL()
  }

  async function saveLoginACLRule(ruleId: string) {
    const draft = loginACLDrafts[ruleId]
    if (!draft) return
    setLoginACLNotice(null)
    setLoginACLSaving(ruleId)
    const res = await api.updateMyLoginACLRule(token, ruleId, {
      pattern: draft.pattern.trim(),
      note: draft.note.trim(),
      position: draft.position,
      active: draft.active,
    })
    setLoginACLSaving(null)
    if (res.error) {
      setLoginACLNotice(res.error.message)
      return
    }
    setLoginACLNotice('Login rule updated.')
    await loadLoginACL()
  }

  async function deleteLoginACLRule(ruleId: string) {
    if (!confirm('Delete this login rule?')) return
    setLoginACLNotice(null)
    setLoginACLSaving(ruleId)
    const res = await api.deleteMyLoginACLRule(token, ruleId)
    setLoginACLSaving(null)
    if (res.error) {
      setLoginACLNotice(res.error.message)
      return
    }
    setLoginACLNotice('Login rule deleted.')
    await loadLoginACL()
  }

  async function setLoginACLEnabled(enabled: boolean) {
    setLoginACLNotice(null)
    const res = await api.setMyLoginACLSettings(token, { enabled })
    if (res.error) {
      setLoginACLNotice(res.error.message)
      return
    }
    setLoginACLNotice(enabled ? 'Login allow-list enabled.' : 'Login allow-list disabled.')
    await loadLoginACL()
  }

  async function submitPrivateProfile(event: FormEvent) {
    event.preventDefault()
    setPrivateNotice(null)
    setPrivateSaving(true)
    const payload = {
      realName: privateDraft.realName.trim(),
      realEmail: privateDraft.realEmail.trim(),
      registrationEmail: privateDraft.registrationEmail.trim(),
      address: privateDraft.address.trim(),
      phone: privateDraft.phone.trim(),
      mobile: privateDraft.mobile.trim(),
      birthday: privateDraft.birthday.trim(),
      school: privateDraft.school.trim(),
      contactNote: privateDraft.contactNote.trim(),
    }
    const res = await api.updateMyPrivateProfile(token, payload)
    setPrivateSaving(false)
    if (res.error) {
      setPrivateNotice(res.error.message)
      return
    }
    setPrivateNotice('Private contact saved.')
    await loadPrivateProfile()
  }

  async function savePersonalFile(name: string, body: string, isPublic: boolean) {
    const fileName = name.trim()
    if (!fileName) return false
    setPersonalFileNotice(null)
    setPersonalFileSaving(fileName)
    const res = await api.saveMyPersonalFile(token, fileName, { body: body.trim(), public: isPublic })
    setPersonalFileSaving(null)
    if (res.error) {
      setPersonalFileNotice(res.error.message)
      return false
    }
    setPersonalFileNotice('Personal file saved.')
    await loadPersonalFiles()
    return true
  }

  async function createPersonalFile(event: FormEvent) {
    event.preventDefault()
    const saved = await savePersonalFile(newPersonalFileName, newPersonalFileBody, newPersonalFilePublic)
    if (saved) {
      setNewPersonalFileName('')
      setNewPersonalFileBody('')
      setNewPersonalFilePublic(true)
    }
  }

  async function deletePersonalFile(name: string) {
    if (!confirm(`Delete ${name}?`)) return
    setPersonalFileNotice(null)
    setPersonalFileSaving(name)
    const res = await api.deleteMyPersonalFile(token, name)
    setPersonalFileSaving(null)
    if (res.error) {
      setPersonalFileNotice(res.error.message)
      return
    }
    setPersonalFileNotice('Personal file deleted.')
    await loadPersonalFiles()
  }

  async function setRegistrationApprovalMode(requireApproval: boolean) {
    setRegistrationNotice(null)
    setRegistrationSaving(true)
    const res = await api.setAccountRegistrationSettings(token, { requireApproval })
    setRegistrationSaving(false)
    if (res.error) {
      setRegistrationNotice(res.error.message)
      return
    }
    setRegistrationSettings(res.data ?? null)
    setRegistrationNotice(requireApproval ? 'Registration approval enabled.' : 'Registration approval disabled.')
    await loadRegistrationAdmin()
  }

  async function reviewRegistration(username: string, decision: 'approved' | 'rejected') {
    const reason = decision === 'rejected' ? prompt('Rejection reason:', '') ?? '' : ''
    setRegistrationNotice(null)
    setRegistrationSaving(true)
    const res = await api.reviewAccountRegistration(token, username, { decision, reason })
    setRegistrationSaving(false)
    if (res.error) {
      setRegistrationNotice(res.error.message)
      return
    }
    setRegistrationNotice(decision === 'approved' ? `${username} approved.` : `${username} rejected.`)
    await loadRegistrationAdmin()
  }

  async function reviewPasswordRecovery(request: PasswordRecoveryRequest, decision: 'reset' | 'rejected') {
    const newPassword = decision === 'reset' ? prompt(`New password for ${request.userName}:`, '') ?? '' : ''
    if (decision === 'reset' && !newPassword) return
    const note = decision === 'rejected' ? prompt('Review note:', '') ?? '' : ''
    setRegistrationNotice(null)
    setRegistrationSaving(true)
    const res = await api.reviewPasswordRecoveryRequest(token, request.id, { decision, newPassword, note })
    setRegistrationSaving(false)
    if (res.error) {
      setRegistrationNotice(res.error.message)
      return
    }
    setRegistrationNotice(decision === 'reset' ? `${request.userName} password reset.` : `${request.userName} recovery rejected.`)
    await loadRegistrationAdmin()
  }

  async function submitTransferID(event: FormEvent) {
    event.preventDefault()
    if (!profile) return
    setTransferNotice(null)
    setTransferSaving(true)
    const res = await api.transferUserId(token, profile.name, transferName.trim())
    setTransferSaving(false)
    if (res.error) {
      setTransferNotice(res.error.message)
      return
    }
    if (!res.data) {
      setTransferNotice('Transfer response missing user.')
      return
    }
    const transferredUser = res.data
    setTransferNotice('User ID transferred.')
    setProfile(prev => prev ? { ...prev, name: transferredUser.name, displayName: prev.displayName === prev.name ? transferredUser.name : prev.displayName } : prev)
    setTransferName(transferredUser.name)
  }

  async function submitHardDelete(event: FormEvent) {
    event.preventDefault()
    if (!profile || isOwnProfile) return
    if (!window.confirm(`Delete ${profile.name}'s account? Public posts will remain under [deleted].`)) return
    setDeleteNotice(null)
    setDeleteSaving(true)
    const res = await api.deleteUser(token, profile.name, deleteReason.trim())
    setDeleteSaving(false)
    if (res.error) {
      setDeleteNotice(res.error.message)
      return
    }
    setDeleteNotice('Account deleted.')
    setProfile(null)
    setError('Account deleted.')
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>
  if (!profile) return <p className="muted">Profile not found.</p>

  const joinDate = new Date(profile.created).toLocaleDateString()
  const lastSeen = profile.lastSeen ? new Date(profile.lastSeen).toLocaleString() : 'Never'
  const trustLabel = TL_LABEL[profile.trustLevel] ?? `TL${profile.trustLevel}`
  const homepageUrl = homepageHref(profile.homepage)

  return (
    <div className="user-profile-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Back</button>
        <h2>User Profile</h2>
        {isOwnProfile && !editMode && (
          <button className="link-btn" onClick={() => setEditMode(true)}>Edit</button>
        )}
        <button className="link-btn" onClick={() => onOpenAuthorPosts(profile.name)}>Read posts</button>
        {!isOwnProfile && (
          <>
            <button className="link-btn" onClick={blessProfile}>Bless</button>
            <button className="link-btn" onClick={() => setRelation('friend', true)}>Add friend</button>
            <button className="link-btn danger" onClick={() => setRelation('ignore', true)}>Ignore</button>
          </>
        )}
      </div>

      {relationNotice && <p className="muted">{relationNotice}</p>}

      <section className="profile-card">
        <div className="profile-identity">
          <div className="profile-avatar" aria-label="avatar">
            {profile.avatar || profile.displayName?.trim()?.[0]?.toUpperCase() || '@'}
          </div>
          <div className="profile-title">
            <h3>{profile.displayName || profile.name}</h3>
            {profile.title && <p className="profile-rank">{profile.title}</p>}
            <p className="muted">@{profile.name}</p>
            {homepageUrl && (
              <a className="profile-homepage" href={homepageUrl} target="_blank" rel="noreferrer">
                {profile.homepage}
              </a>
            )}
            <span className={`trust-badge trust-badge--tl${profile.trustLevel}`} title={`Trust level ${profile.trustLevel}`}>
              {trustLabel}
            </span>
          </div>
        </div>

        <div className="profile-bio">
          <h4>Bio</h4>
          <Markup body={profile.bio || '*No bio yet.*'} />
        </div>

        {profile.plan && (
          <div className="profile-plan">
            <h4>Plan</h4>
            <Markup body={profile.plan} />
          </div>
        )}

        {profile.signature && (
          <div className="profile-signature">
            <h4>Signature</h4>
            <Markup body={profile.signature} />
          </div>
        )}

        <dl className="profile-stats">
          <div>
            <dt>Role</dt>
            <dd>{profile.role}</dd>
          </div>
          <div>
            <dt>Joined</dt>
            <dd>{joinDate}</dd>
          </div>
          <div>
            <dt>Last seen</dt>
            <dd>{lastSeen}</dd>
          </div>
          <div>
            <dt>Posts</dt>
            <dd>{profile.postsCreated}</dd>
          </div>
          <div>
            <dt>Reactions Received</dt>
            <dd>{profile.reactionsReceived}</dd>
          </div>
        </dl>
      </section>

      <section className="profile-pubkeys">
        <h4>SSH pubkeys</h4>
        {profile.pubkeys.length === 0 ? (
          <p className="muted">No SSH keys registered.</p>
        ) : (
          <ul className="profile-key-list">
            {profile.pubkeys.map((pubkey, index) => (
              <li key={`${pubkey}-${index}`}>{pubkey}</li>
            ))}
          </ul>
        )}
      </section>

      <section className="profile-personal-files">
        <div className="profile-section-heading">
          <h4>Personal Files</h4>
          {isOwnProfile && <span className="muted">{personalFiles.length}/16</span>}
        </div>
        {personalFileNotice && <p className={personalFileNotice.includes('saved') || personalFileNotice.includes('deleted') ? 'muted' : 'error'}>{personalFileNotice}</p>}
        {personalFiles.length === 0 ? (
          <p className="muted">No personal files.</p>
        ) : (
          <div className="personal-file-list">
            {personalFiles.map(file => {
              const draft = personalFileDrafts[file.name] ?? { body: file.body, public: file.public }
              return (
                <article key={file.name} className="personal-file-item">
                  <header className="personal-file-header">
                    <h5>{file.name}</h5>
                    {!file.public && <span className="muted">private</span>}
                  </header>
                  <div className="post-body post-body--small">
                    <Markup body={file.body || '*Empty file.*'} />
                  </div>
                  {isOwnProfile && (
                    <div className="personal-file-editor">
                      <label className="inline-toggle">
                        <input
                          type="checkbox"
                          checked={draft.public}
                          onChange={e => setPersonalFileDrafts(prev => ({ ...prev, [file.name]: { ...draft, public: e.target.checked } }))}
                        />
                        Public
                      </label>
                      <textarea
                        value={draft.body}
                        onChange={e => setPersonalFileDrafts(prev => ({ ...prev, [file.name]: { ...draft, body: e.target.value } }))}
                        rows={4}
                      />
                      <div className="form-actions profile-form-actions">
                        <button type="button" onClick={() => savePersonalFile(file.name, draft.body, draft.public)} disabled={personalFileSaving === file.name}>
                          {personalFileSaving === file.name ? 'Saving...' : 'Save'}
                        </button>
                        <button type="button" className="link-btn danger" onClick={() => deletePersonalFile(file.name)}>
                          Delete
                        </button>
                      </div>
                    </div>
                  )}
                </article>
              )
            })}
          </div>
        )}
        {isOwnProfile && (
          <form className="personal-file-new" onSubmit={createPersonalFile}>
            <input value={newPersonalFileName} onChange={e => setNewPersonalFileName(e.target.value)} placeholder="file-name" />
            <label className="inline-toggle">
              <input type="checkbox" checked={newPersonalFilePublic} onChange={e => setNewPersonalFilePublic(e.target.checked)} />
              Public
            </label>
            <textarea value={newPersonalFileBody} onChange={e => setNewPersonalFileBody(e.target.value)} rows={4} placeholder="Personal file body" />
            <div className="form-actions profile-form-actions">
              <button type="submit" disabled={personalFileSaving === newPersonalFileName || !newPersonalFileName.trim()}>
                {personalFileSaving === newPersonalFileName ? 'Saving...' : 'Add file'}
              </button>
            </div>
          </form>
        )}
      </section>

      {isOwnProfile && editMode && (
        <form className="profile-edit-form" onSubmit={submitProfile}>
          <label>
            Display name
            <input value={displayName} onChange={e => setDisplayName(e.target.value)} placeholder="Display name" />
          </label>
          <label>
            Title / rank
            <input value={title} onChange={e => setTitle(e.target.value)} maxLength={80} placeholder="Board veteran, alum, moderator emeritus" />
          </label>
          <label>
            Avatar
            <input value={avatar} onChange={e => setAvatar(e.target.value)} placeholder="Emoji or short ASCII art" />
          </label>
          <label>
            Bio (markup)
            <textarea value={bio} onChange={e => setBio(e.target.value)} rows={5} placeholder="Tell people about yourself" />
          </label>
          <label>
            Homepage
            <input value={homepage} onChange={e => setHomepage(e.target.value)} placeholder="example.edu/~you" />
          </label>
          <label>
            Plan (markup)
            <textarea value={plan} onChange={e => setPlan(e.target.value)} rows={8} placeholder="Longer personal profile or plan" />
          </label>
          <label>
            Signature (markup)
            <textarea value={signature} onChange={e => setSignature(e.target.value)} rows={3} placeholder="Shown under new posts" />
          </label>
          {saveError && <p className="error">{saveError}</p>}
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
            <button className="link-btn" type="button" onClick={() => {
              setEditMode(false)
              if (profile) {
                setDisplayName(profile.displayName)
                setTitle(profile.title)
                setBio(profile.bio)
                setAvatar(profile.avatar)
                setSignature(profile.signature)
                setPlan(profile.plan)
                setHomepage(profile.homepage)
              }
            }}>Cancel</button>
          </div>
        </form>
      )}

      {isOwnProfile && (
        <section className="profile-edit-form">
          <div className="profile-section-heading">
            <h4>Two-factor authentication</h4>
          </div>
          {twoFANotice && <p className="muted">{twoFANotice}</p>}
          <div className="twofa-row">
            <span>Authenticator app (TOTP)</span>
            {twoFAStatus?.totpEnrolled ? (
              <button type="button" className="link-btn danger" onClick={removeTOTP}>Remove</button>
            ) : totpEnroll ? (
              <span className="muted">enrolling…</span>
            ) : (
              <button type="button" onClick={beginTOTP}>Set up</button>
            )}
          </div>
          {totpEnroll && !twoFAStatus?.totpEnrolled && (
            <div className="twofa-enroll">
              <p className="muted">Scan this with your authenticator app, then enter the 6-digit code to confirm.</p>
              <img className="twofa-qr" alt="2FA QR code" src={totpEnroll.qr} />
              <p className="muted">Or enter this key manually: <code>{totpEnroll.secret}</code></p>
              <input value={totpCode} onChange={e => setTotpCode(e.target.value)} inputMode="numeric" placeholder="123456" autoComplete="one-time-code" />
              <div className="form-actions">
                <button type="button" onClick={confirmTOTPCode}>Confirm</button>
                <button type="button" className="link-btn" onClick={() => { setTotpEnroll(null); setTotpCode('') }}>Cancel</button>
              </div>
            </div>
          )}
          <div className="twofa-row">
            <span>Email codes</span>
            {twoFAStatus?.emailEnrolled ? (
              <button type="button" className="link-btn danger" onClick={() => toggleEmail2FA(false)}>Disable</button>
            ) : (
              <button type="button" onClick={() => toggleEmail2FA(true)}>Enable</button>
            )}
          </div>
          <p className="muted">Staff accounts may be required to use two-factor authentication by an administrator.</p>
        </section>
      )}

      {isOwnProfile && (
        <section className="profile-edit-form profile-signature-bank">
          <div className="profile-section-heading">
            <h4>Signatures</h4>
            <div className="profile-section-actions">
              {signatureBundle && <span className="muted">{signatureBundle.signatures.length}/{signatureBundle.maxCount}</span>}
              <button type="button" className="link-btn" onClick={recountSignatures} disabled={signatureSaving === 'recount'}>
                {signatureSaving === 'recount' ? 'Recounting...' : 'Recount'}
              </button>
            </div>
          </div>
          {signatureNotice && <p className={signatureNotice.includes('updated') || signatureNotice.includes('saved') || signatureNotice.includes('deleted') || signatureNotice.includes('refreshed') ? 'muted' : 'error'}>{signatureNotice}</p>}
          {signatureBundle && (
            <>
              <label className="inline-toggle">
                <input
                  type="checkbox"
                  checked={signatureBundle.settings.randomEnabled}
                  onChange={e => setSignatureSelection(signatureBundle.settings.selectedSignatureId, e.target.checked)}
                />
                Random active signature
              </label>
              {!signatureBundle.settings.randomEnabled && signatureBundle.signatures.length > 0 && (
                <label>
                  Current
                  <select
                    value={signatureBundle.settings.selectedSignatureId}
                    onChange={e => setSignatureSelection(e.target.value, false)}
                  >
                    <option value="">First active signature</option>
                    {signatureBundle.signatures.filter(sig => sig.active).map(sig => (
                      <option key={sig.id} value={sig.id}>{sig.label || 'Signature'}</option>
                    ))}
                  </select>
                </label>
              )}
              <div className="signature-bank-list">
                {signatureBundle.signatures.map(sig => {
                  const draft = signatureDrafts[sig.id] ?? { label: sig.label, body: sig.body, active: sig.active, position: sig.position }
                  return (
                    <article key={sig.id} className="signature-bank-item">
                      <div className="signature-bank-row">
                        <input
                          value={draft.label}
                          onChange={e => setSignatureDrafts(prev => ({ ...prev, [sig.id]: { ...draft, label: e.target.value } }))}
                          placeholder="Label"
                        />
                        <label className="inline-toggle">
                          <input
                            type="checkbox"
                            checked={draft.active}
                            onChange={e => setSignatureDrafts(prev => ({ ...prev, [sig.id]: { ...draft, active: e.target.checked } }))}
                          />
                          Active
                        </label>
                      </div>
                      <textarea
                        value={draft.body}
                        onChange={e => setSignatureDrafts(prev => ({ ...prev, [sig.id]: { ...draft, body: e.target.value } }))}
                        rows={3}
                      />
                      <div className="form-actions profile-form-actions">
                        <button type="button" onClick={() => saveSignature(sig.id)} disabled={signatureSaving === sig.id}>
                          {signatureSaving === sig.id ? 'Saving...' : 'Save'}
                        </button>
                        <button type="button" className="link-btn" onClick={() => setSignatureSelection(sig.id, false)} disabled={!draft.active}>
                          Use
                        </button>
                        <button type="button" className="link-btn danger" onClick={() => deleteSignature(sig.id)}>
                          Delete
                        </button>
                      </div>
                    </article>
                  )
                })}
              </div>
            </>
          )}
          <form className="signature-bank-new" onSubmit={createSignature}>
            <input value={newSignatureLabel} onChange={e => setNewSignatureLabel(e.target.value)} placeholder="New label" />
            <textarea value={newSignatureBody} onChange={e => setNewSignatureBody(e.target.value)} rows={3} placeholder="New signature" />
            <div className="form-actions profile-form-actions">
              <button type="submit" disabled={signatureSaving === 'new' || !newSignatureBody.trim()}>
                {signatureSaving === 'new' ? 'Saving...' : 'Add signature'}
              </button>
            </div>
          </form>
        </section>
      )}

      {isOwnProfile && (
        <section className="profile-edit-form profile-login-acl">
          <div className="profile-section-heading">
            <h4>Login Hosts</h4>
            {loginACL?.host && <span className={loginACL.allowed ? 'muted' : 'error'}>{loginACL.host}</span>}
          </div>
          {loginACLNotice && <p className={loginACLNotice.includes('saved') || loginACLNotice.includes('updated') || loginACLNotice.includes('deleted') || loginACLNotice.includes('enabled') || loginACLNotice.includes('disabled') ? 'muted' : 'error'}>{loginACLNotice}</p>}
          {loginACL && (
            <>
              <label className="inline-toggle">
                <input
                  type="checkbox"
                  checked={loginACL.settings.enabled}
                  onChange={e => setLoginACLEnabled(e.target.checked)}
                />
                Restrict logins to active rules
              </label>
              <div className="signature-bank-list">
                {loginACL.rules.map(rule => {
                  const draft = loginACLDrafts[rule.id] ?? { pattern: rule.pattern, note: rule.note, active: rule.active, position: rule.position }
                  return (
                    <article key={rule.id} className="signature-bank-item">
                      <div className="signature-bank-row">
                        <input
                          value={draft.pattern}
                          onChange={e => setLoginACLDrafts(prev => ({ ...prev, [rule.id]: { ...draft, pattern: e.target.value } }))}
                          placeholder="IP, CIDR, or wildcard"
                        />
                        <label className="inline-toggle">
                          <input
                            type="checkbox"
                            checked={draft.active}
                            onChange={e => setLoginACLDrafts(prev => ({ ...prev, [rule.id]: { ...draft, active: e.target.checked } }))}
                          />
                          Active
                        </label>
                      </div>
                      <input
                        value={draft.note}
                        onChange={e => setLoginACLDrafts(prev => ({ ...prev, [rule.id]: { ...draft, note: e.target.value } }))}
                        placeholder="Note"
                      />
                      <div className="form-actions profile-form-actions">
                        <button type="button" onClick={() => saveLoginACLRule(rule.id)} disabled={loginACLSaving === rule.id}>
                          {loginACLSaving === rule.id ? 'Saving...' : 'Save'}
                        </button>
                        <button type="button" className="link-btn danger" onClick={() => deleteLoginACLRule(rule.id)}>Delete</button>
                      </div>
                    </article>
                  )
                })}
              </div>
            </>
          )}
          <form className="signature-bank-new" onSubmit={createLoginACLRule}>
            <input value={newLoginACLPattern} onChange={e => setNewLoginACLPattern(e.target.value)} placeholder="203.0.113.7 or 203.0.113.0/24" />
            <input value={newLoginACLNote} onChange={e => setNewLoginACLNote(e.target.value)} placeholder="Note" />
            <div className="form-actions profile-form-actions">
              <button type="submit" disabled={loginACLSaving === 'new' || !newLoginACLPattern.trim()}>
                {loginACLSaving === 'new' ? 'Saving...' : 'Add host rule'}
              </button>
            </div>
          </form>
        </section>
      )}

      {isOwnProfile && (
        <form className="profile-edit-form profile-private-profile" onSubmit={submitPrivateProfile}>
          <h4>Private Contact</h4>
          {privateNotice && <p className={privateNotice === 'Private contact saved.' ? 'muted' : 'error'}>{privateNotice}</p>}
          <div className="profile-private-grid">
            <label>
              Real name
              <input value={privateDraft.realName} onChange={e => setPrivateDraft(prev => ({ ...prev, realName: e.target.value }))} />
            </label>
            <label>
              Real email
              <input value={privateDraft.realEmail} onChange={e => setPrivateDraft(prev => ({ ...prev, realEmail: e.target.value }))} />
            </label>
            <label>
              Registration email
              <input value={privateDraft.registrationEmail} onChange={e => setPrivateDraft(prev => ({ ...prev, registrationEmail: e.target.value }))} />
            </label>
            <label>
              Birthday
              <input value={privateDraft.birthday} onChange={e => setPrivateDraft(prev => ({ ...prev, birthday: e.target.value }))} />
            </label>
            <label>
              Phone
              <input value={privateDraft.phone} onChange={e => setPrivateDraft(prev => ({ ...prev, phone: e.target.value }))} />
            </label>
            <label>
              Mobile
              <input value={privateDraft.mobile} onChange={e => setPrivateDraft(prev => ({ ...prev, mobile: e.target.value }))} />
            </label>
            <label>
              School
              <input value={privateDraft.school} onChange={e => setPrivateDraft(prev => ({ ...prev, school: e.target.value }))} />
            </label>
          </div>
          <label>
            Address
            <textarea value={privateDraft.address} onChange={e => setPrivateDraft(prev => ({ ...prev, address: e.target.value }))} rows={2} />
          </label>
          <label>
            Contact note
            <textarea value={privateDraft.contactNote} onChange={e => setPrivateDraft(prev => ({ ...prev, contactNote: e.target.value }))} rows={3} />
          </label>
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={privateSaving}>{privateSaving ? 'Saving...' : 'Save private contact'}</button>
            <button type="button" className="link-btn" onClick={() => setPrivateDraft(privateProfile ? privateDraftFromProfile(privateProfile) : emptyPrivateDraft())}>
              Reset
            </button>
          </div>
        </form>
      )}

      {isOwnProfile && currentUserRole === 'admin' && (
        <section className="profile-edit-form profile-registration-admin">
          <div className="profile-section-heading">
            <h4>Registration</h4>
            <label className="inline-toggle">
              <input
                type="checkbox"
                checked={Boolean(registrationSettings?.requireApproval)}
                onChange={e => setRegistrationApprovalMode(e.target.checked)}
                disabled={registrationSaving || !registrationSettings}
              />
              Require approval
            </label>
          </div>
          {registrationNotice && <p className={registrationNotice.includes('enabled') || registrationNotice.includes('disabled') || registrationNotice.includes('approved') || registrationNotice.includes('rejected') ? 'muted' : 'error'}>{registrationNotice}</p>}
          <div className="registration-review-list">
            {pendingRegistrations.length === 0 ? (
              <p className="muted">No pending registrations.</p>
            ) : pendingRegistrations.map(row => (
              <article key={row.id} className="registration-review-item">
                <div>
                  <strong>{row.name}</strong>
                  <p className="muted">{new Date(row.created).toLocaleString()}</p>
                  {(row.realName || row.affiliation || row.email) && (
                    <p className="muted">{[row.realName, row.affiliation, row.email].filter(Boolean).join(' · ')}</p>
                  )}
                  {row.note && <p className="muted">{row.note}</p>}
                </div>
                <div className="form-actions profile-form-actions">
                  <button type="button" onClick={() => reviewRegistration(row.name, 'approved')} disabled={registrationSaving}>Approve</button>
                  <button type="button" className="link-btn danger" onClick={() => reviewRegistration(row.name, 'rejected')} disabled={registrationSaving}>Reject</button>
                </div>
              </article>
            ))}
          </div>
          <div className="profile-section-heading">
            <h4>Password Recovery</h4>
            <span className="muted">{passwordRecoveryRequests.length}</span>
          </div>
          <div className="registration-review-list">
            {passwordRecoveryRequests.length === 0 ? (
              <p className="muted">No pending recovery requests.</p>
            ) : passwordRecoveryRequests.map(row => (
              <article key={row.id} className="registration-review-item">
                <div>
                  <strong>{row.userName}</strong>
                  <p className="muted">{row.submittedName || 'No real name'} / {row.submittedEmail || 'No email'}</p>
                  {row.note && <p className="muted">{row.note}</p>}
                </div>
                <div className="form-actions profile-form-actions">
                  <button type="button" onClick={() => reviewPasswordRecovery(row, 'reset')} disabled={registrationSaving}>Reset</button>
                  <button type="button" className="link-btn danger" onClick={() => reviewPasswordRecovery(row, 'rejected')} disabled={registrationSaving}>Reject</button>
                </div>
              </article>
            ))}
          </div>
        </section>
      )}

      {currentUserRole === 'admin' && (
        <form className="profile-edit-form profile-transfer-id" onSubmit={submitTransferID}>
          <h4>Transfer ID</h4>
          <label>
            Login name
            <input value={transferName} onChange={e => setTransferName(e.target.value)} />
          </label>
          {transferNotice && <p className={transferNotice === 'User ID transferred.' ? 'muted' : 'error'}>{transferNotice}</p>}
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={transferSaving || !transferName.trim() || transferName === profile.name}>
              {transferSaving ? 'Transferring...' : 'Transfer'}
            </button>
          </div>
        </form>
      )}

      {currentUserRole === 'admin' && !isOwnProfile && (
        <form className="profile-edit-form profile-danger-zone" onSubmit={submitHardDelete}>
          <h4>Delete account</h4>
          <label>
            Admin note
            <textarea value={deleteReason} onChange={e => setDeleteReason(e.target.value)} rows={3} />
          </label>
          {deleteNotice && <p className={deleteNotice === 'Account deleted.' ? 'muted' : 'error'}>{deleteNotice}</p>}
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={deleteSaving}>
              {deleteSaving ? 'Deleting...' : 'Delete account'}
            </button>
          </div>
        </form>
      )}

      {isOwnProfile && (
        <form className="profile-edit-form" onSubmit={submitPassword}>
          <h4>Password</h4>
          <label>
            Current password
            <input type="password" value={currentPassword} onChange={e => setCurrentPassword(e.target.value)} autoComplete="current-password" />
          </label>
          <label>
            New password
            <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} autoComplete="new-password" />
          </label>
          <label>
            Confirm new password
            <input type="password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} autoComplete="new-password" />
          </label>
          {passwordNotice && <p className={passwordNotice === 'Password updated.' ? 'muted' : 'error'}>{passwordNotice}</p>}
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={passwordSaving || !currentPassword || !newPassword || !confirmPassword}>
              {passwordSaving ? 'Updating...' : 'Update password'}
            </button>
          </div>
        </form>
      )}

      {isOwnProfile && (
        <form className="profile-edit-form profile-danger-zone" onSubmit={submitDeactivate}>
          <h4>Close account</h4>
          <label>
            Password
            <input type="password" value={deactivatePassword} onChange={e => setDeactivatePassword(e.target.value)} autoComplete="current-password" />
          </label>
          <label>
            Private note
            <textarea value={deactivateReason} onChange={e => setDeactivateReason(e.target.value)} rows={3} />
          </label>
          {deactivateNotice && <p className={deactivateNotice === 'Account closed.' ? 'muted' : 'error'}>{deactivateNotice}</p>}
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={deactivating || !deactivatePassword}>
              {deactivating ? 'Closing...' : 'Close account'}
            </button>
          </div>
        </form>
      )}

      <section className="profile-recent-posts">
        <h4>Recent Posts</h4>
        {loadingPosts ? (
          <Spinner />
        ) : recentPosts.length === 0 ? (
          <p className="muted">No visible posts yet.</p>
        ) : (
          <div className="recent-posts">
            {recentPosts.map(post => (
              <article key={post.id} className="recent-post-card">
                <header className="recent-post-meta">
                  <span className="muted">Thread {post.thread}</span>
                  <span className="muted">#{post.createdSeq}</span>
                </header>
                <div className="post-body post-body--small">
                  <Markup body={post.body} redacted={post.redacted} />
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
