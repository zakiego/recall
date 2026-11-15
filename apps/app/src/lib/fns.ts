// The dashboard's server functions: thin wrappers over the Go server's /app/api/rpc/<name>, with the
// same export names and call signatures the TanStack server functions had,
// so call sites don't change. The types come from the old functions in
// src/server/fns/ (type-only, erased from the bundle); M10b moves them here
// before src/server goes.
import { rpcFn } from "@/lib/rpc-client"
import type * as F from "@/server/fns/fns"

// The Upcoming page's windows (days).
export const UPCOMING_WINDOWS = [7, 30, 90, 365] as const

export const getSession = rpcFn<typeof F.getSession>("getSession", "GET")
export const switchWorkspace = rpcFn<typeof F.switchWorkspace>("switchWorkspace", "POST")
export const newWorkspace = rpcFn<typeof F.newWorkspace>("newWorkspace", "POST")
export const getContainers = rpcFn<typeof F.getContainers>("getContainers", "GET")
export const getMemories = rpcFn<typeof F.getMemories>("getMemories", "GET")
export const getChain = rpcFn<typeof F.getChain>("getChain", "GET")
export const updateMemory = rpcFn<typeof F.updateMemory>("updateMemory", "POST")
export const resolveMemory = rpcFn<typeof F.resolveMemory>("resolveMemory", "POST")
export const deleteMemory = rpcFn<typeof F.deleteMemory>("deleteMemory", "POST")
export const getUpcoming = rpcFn<typeof F.getUpcoming>("getUpcoming", "GET")
export const getReminders = rpcFn<typeof F.getReminders>("getReminders", "GET")
export const saveReminders = rpcFn<typeof F.saveReminders>("saveReminders", "POST")
export const sendReminderTest = rpcFn<typeof F.sendReminderTest>("sendReminderTest", "POST")
export const getPeople = rpcFn<typeof F.getPeople>("getPeople", "GET")
export const getMergeSuggestions = rpcFn<typeof F.getMergeSuggestions>("getMergeSuggestions", "GET")
export const getPersonPage = rpcFn<typeof F.getPersonPage>("getPersonPage", "GET")
export const savePerson = rpcFn<typeof F.savePerson>("savePerson", "POST")
export const mergePerson = rpcFn<typeof F.mergePerson>("mergePerson", "POST")
export const removePerson = rpcFn<typeof F.removePerson>("removePerson", "POST")
export const searchMemories = rpcFn<typeof F.searchMemories>("searchMemories", "POST")
export const askMemory = rpcFn<typeof F.askMemory>("askMemory", "POST")
export const getDocuments = rpcFn<typeof F.getDocuments>("getDocuments", "GET")
export const addText = rpcFn<typeof F.addText>("addText", "POST")
export const uploadFile = rpcFn<typeof F.uploadFile>("uploadFile", "POST")
export const retryDocument = rpcFn<typeof F.retryDocument>("retryDocument", "POST")
export const removeDocument = rpcFn<typeof F.removeDocument>("removeDocument", "POST")
export const getGraph = rpcFn<typeof F.getGraph>("getGraph", "GET")
export const getKeys = rpcFn<typeof F.getKeys>("getKeys", "GET")
export const getConnectInfo = rpcFn<typeof F.getConnectInfo>("getConnectInfo", "GET")
export const createKey = rpcFn<typeof F.createKey>("createKey", "POST")
export const revokeKey = rpcFn<typeof F.revokeKey>("revokeKey", "POST")
export const getMembers = rpcFn<typeof F.getMembers>("getMembers", "GET")
export const createInviteLink = rpcFn<typeof F.createInviteLink>("createInviteLink", "POST")
export const revokeInviteLink = rpcFn<typeof F.revokeInviteLink>("revokeInviteLink", "POST")
export const setMemberRole = rpcFn<typeof F.setMemberRole>("setMemberRole", "POST")
export const removeWorkspaceMember = rpcFn<typeof F.removeWorkspaceMember>("removeWorkspaceMember", "POST")
export const getInvite = rpcFn<typeof F.getInvite>("getInvite", "GET")
export const acceptInvite = rpcFn<typeof F.acceptInvite>("acceptInvite", "POST")
export const acceptInviteNewAccount = rpcFn<typeof F.acceptInviteNewAccount>("acceptInviteNewAccount", "POST")
export const getAccount = rpcFn<typeof F.getAccount>("getAccount", "GET")
export const removePasskey = rpcFn<typeof F.removePasskey>("removePasskey", "POST")
