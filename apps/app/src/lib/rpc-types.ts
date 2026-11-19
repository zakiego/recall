// The shapes the Go server's RPC endpoints (/app/api/rpc/<name>) take and
// return, one Input and one Output type per function. They were generated from
// the TanStack server functions they replace (the TS backend, removed in the
// Go port), with each zod validator written out as the plain type it accepted.
// The Go side is apps/server/internal/httpapi/rpc_*.go; change both together.

export type Role = "owner" | "admin" | "member"

export type DatePrecision = "day" | "month" | "year"

export type Membership = { id: string; name: string; slug: string; role: Role }

export type Viewer = {
  sessionId: string
  user: { id: string; username: string; name: string }
  workspace: Membership | null
  workspaces: Membership[]
}

export type PersonRef = { id: string; name: string }

export type Person = {
  id: string
  workspaceId: string
  container: string
  name: string
  aliases: string[]
  role: string | null
  organization: string | null
  emails: string[]
  handles: string[]
  summary: string
  manual: boolean
  createdAt: number
  updatedAt: number
}

export type PersonRow = Person & {
  lastMentionedAt: number | null
  factCount: number
  latestFact: string | null
}

export type PersonFact = {
  id: string
  fact: string
  kind: "static" | "dynamic"
  createdAt: number
  validTo: number | null
  supersedesId: string | null
  reviewNote: string | null
  eventDate: string | null
  eventPrecision: DatePrecision | null
  dueDate: string | null
  duePrecision: DatePrecision | null
  source: {
    id: string
    title: string | null
    type: string
    url: string | null
  } | null
}

export type ReminderSettings = {
  enabled: boolean
  email: string | null
  leadDays: number[]
  timezone: string
  updatedAt: number | null
}

export type AddResult = {
  id: string
  container: string
  status: "queued" | "processing" | "done" | "failed"
  sourceId: string | null
  duplicate: boolean
}

// A fact or excerpt the model was shown. `ref` is the footnote number: "[2]"
// in the answer is the citation with ref 2.
export type Citation = (
  | { type: "memory"; id: string; text: string }
  | { type: "chunk"; id: string; text: string; documentId: string; documentTitle: string | null }
) & { ref: number }

export type AskResponse = {
  answer: string
  covered: boolean
  citations: Citation[]
  model: string | null
  people: PersonRef[]
  container: string
  question: string
  timingMs: number
}

export type AppProvider = "oslo" | "finansial"

export type AppConnectorView = {
  provider: AppProvider
  name: string
  configured: boolean
  container: string | null
  baseUrl: string
  hasKey: boolean
  enabled: boolean
  lastSyncAt: number | null
  lastSuccessAt: number | null
  lastError: string | null
  caughtUp: boolean
  thresholds: Record<string, number> | null
}

export type GmailResult = {
  id: string
  threadId: string | null
  from: string
  subject: string
  snippet: string
  date: number | null
  imported: boolean
  importFailed: boolean
}

export type ConsentRequest =
  | { ok: false; problem: string; signedIn: boolean }
  | {
      ok: true
      client: { name: string; uri: string | null; redirectHost: string }
      scopes: string[]
      username: string
      workspaces: { id: string; name: string }[]
      activeWorkspaceId: string | null
    }

// --- fns.ts ---------------------------------------------------------------

export type GetSessionOutput = {
  viewer: Viewer | null
  today: string
  configured: boolean
  hasUsers: boolean
  defaultContainer: string
}

export type SwitchWorkspaceInput = {
  id: string
}

export type SwitchWorkspaceOutput = {
  ok: true
}

export type NewWorkspaceInput = {
  name: string
}

export type NewWorkspaceOutput = {
  id: string
  name: string
}

export type GetContainersOutput = {
  containers: {
    container: string
    memories: number
    documents: number
    lastActivity: number
  }[]
  defaultContainer: string
}

export type GetMemoriesInput = {
  container: string
  cursor?: string
  history?: boolean
  review?: boolean
  limit?: number
}

export type GetMemoriesOutput = {
  memories: {
    id: string
    workspaceId: string
    container: string
    fact: string
    keywords: string
    kind: "dynamic" | "static"
    sourceDocumentId: string | null
    supersedesId: string | null
    validFrom: number
    validTo: number | null
    createdAt: number
    reviewNote: string | null
    conflictsWithId: string | null
    eventDate: string | null
    eventPrecision: "day" | "month" | "year" | null
    dueDate: string | null
    duePrecision: "day" | "month" | "year" | null
    versions: number
    sourceTitle: string | null
    conflictWith: {
      id: string
      fact: string
      current: boolean
    } | null
    people: PersonRef[]
  }[]
  nextCursor: string | null
  reviewCount: number
  profile: {
    container: string
    static: string
    dynamic: string
    updatedAt: number | null
    staticMemories: {
      id: string
      workspaceId: string
      container: string
      fact: string
      keywords: string
      kind: "dynamic" | "static"
      sourceDocumentId: string | null
      supersedesId: string | null
      validFrom: number
      validTo: number | null
      createdAt: number
      reviewNote: string | null
      conflictsWithId: string | null
      eventDate: string | null
      eventPrecision: "day" | "month" | "year" | null
      dueDate: string | null
      duePrecision: "day" | "month" | "year" | null
    }[]
    dynamicMemories: {
      id: string
      workspaceId: string
      container: string
      fact: string
      keywords: string
      kind: "dynamic" | "static"
      sourceDocumentId: string | null
      supersedesId: string | null
      validFrom: number
      validTo: number | null
      createdAt: number
      reviewNote: string | null
      conflictsWithId: string | null
      eventDate: string | null
      eventPrecision: "day" | "month" | "year" | null
      dueDate: string | null
      duePrecision: "day" | "month" | "year" | null
    }[]
  }
}

export type GetChainInput = {
  id: string
}

export type GetChainOutput = {
  firstVersion: number
  hasNewer: boolean
  versions: {
    sourceTitle: string | null
    conflictsWithId: string | null
    container: string
    createdAt: number
    dueDate: string | null
    duePrecision: "day" | "month" | "year" | null
    eventDate: string | null
    eventPrecision: "day" | "month" | "year" | null
    fact: string
    id: string
    keywords: string
    kind: "dynamic" | "static"
    reviewNote: string | null
    sourceDocumentId: string | null
    supersedesId: string | null
    validFrom: number
    validTo: number | null
    workspaceId: string
  }[]
}

export type UpdateMemoryInput = {
  id: string
  fact: string
  keywords?: string
  kind?: "dynamic" | "static"
  eventDate?: string | null
  dueDate?: string | null
}

export type UpdateMemoryOutput = {
  conflictsWithId: string | null
  container: string
  createdAt: number
  dueDate: string | null
  duePrecision: "day" | "month" | "year" | null
  eventDate: string | null
  eventPrecision: "day" | "month" | "year" | null
  fact: string
  id: string
  keywords: string
  kind: "dynamic" | "static"
  reviewNote: string | null
  sourceDocumentId: string | null
  supersedesId: string | null
  validFrom: number
  validTo: number | null
  workspaceId: string
}

export type ResolveMemoryInput = {
  id: string
}

export type ResolveMemoryOutput = {
  conflictsWithId: string | null
  container: string
  createdAt: number
  dueDate: string | null
  duePrecision: "day" | "month" | "year" | null
  eventDate: string | null
  eventPrecision: "day" | "month" | "year" | null
  fact: string
  id: string
  keywords: string
  kind: "dynamic" | "static"
  reviewNote: string | null
  sourceDocumentId: string | null
  supersedesId: string | null
  validFrom: number
  validTo: number | null
  workspaceId: string
}

export type DeleteMemoryInput = {
  id: string
}

export type DeleteMemoryOutput = {
  id: string
  workspaceId: string
  container: string
  fact: string
  keywords: string
  kind: "dynamic" | "static"
  sourceDocumentId: string | null
  supersedesId: string | null
  validFrom: number
  validTo: number | null
  createdAt: number
  reviewNote: string | null
  conflictsWithId: string | null
  eventDate: string | null
  eventPrecision: "day" | "month" | "year" | null
  dueDate: string | null
  duePrecision: "day" | "month" | "year" | null
}

export type GetUpcomingInput = {
  container: string
  days: number
  all?: boolean
}

export type GetUpcomingOutput = {
  today: string
  memories: {
    id: string
    workspaceId: string
    container: string
    fact: string
    keywords: string
    kind: "dynamic" | "static"
    sourceDocumentId: string | null
    supersedesId: string | null
    validFrom: number
    validTo: number | null
    createdAt: number
    reviewNote: string | null
    conflictsWithId: string | null
    eventDate: string | null
    eventPrecision: "day" | "month" | "year" | null
    dueDate: string | null
    duePrecision: "day" | "month" | "year" | null
    // null for month and year precision (and a hand-edited row that isn't YYYY-MM-DD)
    daysLeft: number | null
    versions: number
  }[]
  counts: Record<7 | 30 | 90 | 365, number>
  total: number
  pastDays: 30
  reminders: {
    enabled: boolean
    email: string | null
    leadDays: number[]
    timezone: string
    configured: boolean
  }
}

export type GetRemindersOutput = {
  settings: ReminderSettings
  configured: boolean
  from: string | null
}

export type SaveRemindersInput = {
  enabled: boolean
  email: string | ""
  leadDays: number[]
  timezone: string
}

export type SaveRemindersOutput = ReminderSettings

export type SendReminderTestOutput = {
  id: string
  to: string
  items: number
}

export type GetPeopleInput = {
  container: string
  q?: string
  sort?: "name" | "recent"
}

export type GetPeopleOutput = {
  people: PersonRow[]
  total: number
  nextOffset: number | null
}

export type GetMergeSuggestionsInput = {
  container: string
}

export type GetMergeSuggestionsOutput = {
  keep: {
    id: string
    name: string
    role: string | null
    organization: string | null
    factCount: number
  }
  drop: {
    id: string
    name: string
    role: string | null
    organization: string | null
    factCount: number
  }
  reasons: string[]
  similarity: number | null
}[]

export type GetPersonPageInput = {
  id: string
  history?: boolean
}

export type GetPersonPageOutput = {
  person: PersonRow
  facts: PersonFact[]
  others: {
    id: string
    name: string
    role: string | null
    organization: string | null
    factCount: number
  }[]
} | null

export type SavePersonInput = {
  id: string
  name: string
  aliases: string[]
  role: string | null
  organization: string | null
  summary: string
  emails: string[]
  handles: string[]
}

export type SavePersonOutput = PersonRow

export type MergePersonInput = {
  keepId: string
  dropId: string
}

export type MergePersonOutput = PersonRow

export type RemovePersonInput = {
  id: string
}

export type RemovePersonOutput = {
  ok: true
}

export type SearchMemoriesInput = {
  container: string
  q: string
  chunks?: boolean
  limit?: number
}

export type SearchMemoriesOutput = {
  container: string
  q: string
  results: ({
    type: "chunk"
    id: string
    text: string
    documentId: string
    documentTitle: string | null
    idx: number
    score: number
    ftsRank?: number
    vecRank?: number
    similarity?: number
  } | {
    type: "memory"
    id: string
    text: string
    kind: "static" | "dynamic"
    keywords: string
    sourceDocumentId: string | null
    createdAt: number
    eventDate: string | null
    eventPrecision: DatePrecision | null
    dueDate: string | null
    duePrecision: DatePrecision | null
    score: number
    ftsRank?: number
    vecRank?: number
    similarity?: number
    people: PersonRef[]
  })[]
  timingMs: number
}

export type AskMemoryInput = {
  container: string
  question: string
  chunks?: boolean
}

export type AskMemoryOutput = AskResponse

export type GetDocumentsInput = {
  container: string
  cursor?: string
  limit?: number
}

export type GetDocumentsOutput = {
  nextCursor: string | null
  total: number | null
  documents: {
    id: string
    container: string
    sourceType: "chat" | "email" | "file" | "text" | "url"
    title: string | null
    url: string | null
    status: "done" | "failed" | "processing" | "queued"
    error: string | null
    createdAt: number
    preview: string
    memoryCount: number
    extract: {
      added: number
      updated: number
      invalidated: number
      conflicts: number
    } | null
  }[]
}

export type AddTextInput = {
  container: string
  kind: "text" | "url"
  value: string
  title?: string
}

export type AddTextOutput = AddResult

export type UploadFileInput = FormData

export type UploadFileOutput = AddResult

export type RetryDocumentInput = {
  id: string
}

export type RetryDocumentOutput = {
  ok: true
}

export type RemoveDocumentInput = {
  id: string
}

export type RemoveDocumentOutput = {
  ok: true
}

export type GetGraphInput = {
  container: string
}

export type GetGraphOutput = {
  documents: {
    id: string
    title: string
    summary: null
    documentType: "chat" | "email" | "file" | "text" | "url"
    createdAt: string
    updatedAt: string
    memories: {
      id: string
      memory: string
      content: string
      isStatic: boolean
      spaceId: string
      isLatest: boolean
      isForgotten: boolean
      forgetAfter: null
      forgetReason: null
      version: number
      parentMemoryId: string | null
      rootMemoryId: string
      createdAt: string
      updatedAt: string
      relation: "updates" | null
      updatesMemoryId: string | null
      nextVersionId: string | null
      spaceContainerTag: string
    }[]
  }[]
  shownDocuments: number
  totalDocuments: number
  capped: boolean
  hiddenFacts: number
}

export type GetKeysOutput = {
  keys: {
    id: string
    name: string
    prefix: string
    createdAt: number
    lastUsedAt: number | null
    revokedAt: number | null
    createdBy: string | null
  }[]
  canManage: boolean
}

export type GetConnectInfoOutput = {
  origin: string
  canManage: boolean
}

export type CreateKeyInput = {
  name: string
}

export type CreateKeyOutput = {
  key: string
  id: string
  name: string
  prefix: string
}

export type RevokeKeyInput = {
  id: string
}

export type RevokeKeyOutput = {
  ok: boolean
}

export type GetMembersOutput = {
  members: {
    memberId: string
    userId: string
    username: string | null
    displayUsername: string | null
    name: string
    role: Role
    joinedAt: number
  }[]
  invites: {
    id: string
    role: "admin" | "member" | "owner"
    createdAt: number
    expiresAt: number
    createdBy: string | null
  }[]
  myRole: "admin" | "member" | "owner"
  myUserId: string
  canManage: boolean
}

export type CreateInviteLinkInput = {
  role: "admin" | "member" | "owner"
}

export type CreateInviteLinkOutput = {
  url: string
  id: string
  role: "admin" | "member" | "owner"
  expiresAt: number
}

export type RevokeInviteLinkInput = {
  id: string
}

export type RevokeInviteLinkOutput = {
  ok: boolean
}

export type SetMemberRoleInput = {
  memberId: string
  role: "admin" | "member" | "owner"
}

export type SetMemberRoleOutput = {
  ok: true
}

export type RemoveWorkspaceMemberInput = {
  memberId: string
}

export type RemoveWorkspaceMemberOutput = {
  ok: true
  left: boolean
}

export type GetInviteInput = {
  token: string
}

export type GetInviteOutput = {
  ok: false
  problem: "This invite link expired. Ask for a new one." | "This invite link isn't valid." | "This invite link was already used." | "This invite link was revoked."
  signedInAs: string | null
  workspace?: undefined
  role?: undefined
  expiresAt?: undefined
  alreadyMember?: undefined
} | {
  problem?: undefined
  ok: true
  workspace: string
  role: "admin" | "member" | "owner"
  expiresAt: number
  signedInAs: string | null
  alreadyMember: boolean
}

export type AcceptInviteInput = {
  token: string
}

export type AcceptInviteOutput = {
  ok: true
  workspace: string
}

export type AcceptInviteNewAccountInput = {
  token: string
  username: string
  password: string
}

export type AcceptInviteNewAccountOutput = {
  ok: true
  workspace: string
}

export type GetAccountOutput = {
  username: string
  passkeys: {
    id: string
    name: string | null
    deviceType: string
    backedUp: boolean
    createdAt: number | null
  }[]
}

export type RemovePasskeyInput = {
  id: string
}

export type RemovePasskeyOutput = {
  ok: true
}

// --- connector-fns.ts -----------------------------------------------------

export type GetConnectorsInput = {
  container: string
}

export type GetConnectorsOutput = {
  configured: boolean
  missing: string[]
  canManage: boolean
  gmail: {
    email: string | null
    status: "active" | "reauth"
    connectedAt: number
    lastUsedAt: number | null
  } | null
  connections: {
    id: string
    userId: string
    status: "active" | "reauth"
    createdAt: number
    lastUsedAt: number | null
    username: string
    mine: boolean
    accountEmail: string | null
  }[]
  imports: {
    id: string
    sourceType: "chat" | "email" | "file" | "text" | "url"
    sourceId: string | null
    title: string | null
    status: "done" | "failed" | "processing" | "queued"
    error: string | null
    createdAt: number
    canRetry: boolean
  }[]
  maxImport: 50
  apps: AppConnectorView[]
  appSecretConfigured: boolean
}

export type SearchGmailMessagesInput = {
  container: string
  q: string
  pageToken?: string
}

export type SearchGmailMessagesOutput = {
  messages: GmailResult[]
  nextPageToken: string | null
  estimate: number
}

export type ImportGmailMessagesInput = {
  container: string
  messages: {
    id: string
    subject?: string | null
  }[]
}

export type ImportGmailMessagesOutput = {
  queued: number
  duplicates: number
}

export type DisconnectGmailOutput = {
  removed: boolean
  revoked: boolean
}

export type SaveAppConnectorFnInput = {
  provider: "finansial" | "oslo"
  container: string
  baseUrl: string
  apiKey?: string | null
  enabled: boolean
  thresholds?: Record<string, number>
}

export type SaveAppConnectorFnOutput = AppConnectorView

export type SyncAppConnectorFnInput = {
  provider: "finansial" | "oslo"
}

export type SyncAppConnectorFnOutput = {
  ok: boolean
  items: number
  documents: number
  staged: number
  caughtUp: boolean
  error: string | null
}

// --- oauth-fns.ts ---------------------------------------------------------

export type GetConsentRequestInput = {
  query: string
}

export type GetConsentRequestOutput = ConsentRequest
